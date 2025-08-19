package ocm

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/utils/lru"
	"ocm.software/ocm/api/datacontext"
	"ocm.software/ocm/api/ocm"
	"ocm.software/ocm/api/ocm/extensions/attrs/signingattr"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

var (
	// Prometheus gauges tracking cache sizes for sessions and contexts.
	sessionCacheSize *prometheus.GaugeVec
	contextCacheSize *prometheus.GaugeVec
)

// MustRegisterMetrics registers metrics and panics on error.
// Intended to be called during process startup.
func MustRegisterMetrics(registerer prometheus.Registerer) {
	if err := RegisterMetrics(registerer); err != nil {
		panic(err)
	}
}

// RegisterMetrics registers Prometheus metrics for session/context cache sizes.
// Uses errors.Join to return multiple registration errors if they occur.
func RegisterMetrics(registerer prometheus.Registerer) error {
	return errors.Join(
		registerer.Register(sessionCacheSize),
		registerer.Register(contextCacheSize),
	)
}

// init initializes Prometheus metric definitions.
func init() {
	sessionCacheSize = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "session_cache_size",
		Help: "number of objects in cache",
	}, []string{"name"})
	contextCacheSize = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "context_cache_size",
		Help: "number of objects in cache",
	}, []string{"name"})
}

// Compile-time assertion that ContextCache implements manager.Runnable.
var _ manager.Runnable = (*ContextCache)(nil)

// NewContextCache constructs a ContextCache with given name, size, and k8s client.
func NewContextCache(name string, contextCacheSize, sessionCacheSize int, client ctrl.Client) *ContextCache {
	return &ContextCache{
		name:             name,
		contextCacheSize: contextCacheSize,
		sessionCacheSize: sessionCacheSize,
		lookupClient:     client,
	}
}

// ContextCache holds LRU caches for OCM contexts and sessions.
// Contexts are expensive to build/configure, sessions are bound to repositories.
// Eviction handlers clean up resources when entries are removed.
type ContextCache struct {
	ctx                                context.Context //nolint:containedctx // context is passed via Start from manager
	name                               string
	contextCacheSize, sessionCacheSize int

	contexts *lru.Cache // cache of ocm.Context objects
	sessions *lru.Cache // cache of ocm.Session objects

	lookupClient ctrl.Reader // k8s client used for fetching configuration
}

// Start initializes caches with eviction functions and blocks until context is done.
// When the provided context is canceled, all cached contexts and sessions are cleared.
func (m *ContextCache) Start(ctx context.Context) error {
	if m.ctx != nil {
		return fmt.Errorf("already started")
	}

	logger := log.FromContext(ctx)

	// Cache for contexts, with eviction finalizer.
	m.contexts = lru.NewWithEvictionFunc(m.contextCacheSize, func(key lru.Key, value interface{}) {
		defer contextCacheSize.WithLabelValues(m.name).Dec()
		ctx := value.(ocm.Context) //nolint:forcetypeassert // safe cast
		if err := ctx.Finalize(); err != nil {
			logger.Error(err, "failed to finalize context", "key", key)
		}
	})

	// Cache for sessions, with eviction finalizer.
	m.sessions = lru.NewWithEvictionFunc(m.sessionCacheSize, func(key lru.Key, value interface{}) {
		defer sessionCacheSize.WithLabelValues(m.name).Dec()
		session := value.(ocm.Session) //nolint:forcetypeassert // safe cast
		if err := session.Close(); err != nil {
			logger.Error(err, "failed to close session", "key", key)
		}
	})

	m.ctx = ctx

	// Watch for context cancellation to clear caches.
	done := make(chan struct{}, 1)
	go func() {
		<-ctx.Done()
		m.sessions.Clear()
		m.contexts.Clear()
		done <- struct{}{}
		close(done)
	}()

	<-done

	return nil
}

// GetSessionOptions encapsulates all parameters required to obtain or create an OCM session.
type GetSessionOptions struct {
	// RepositorySpecification is the repository specification for the session.
	RepositorySpecification *apiextensionsv1.JSON
	// OCMConfigurations is the list of OCM configurations to use for the session.
	OCMConfigurations []v1alpha1.OCMConfiguration
	// VerificationProvider is the optional additional provider that provides verification information for signatures.
	VerificationProvider v1alpha1.VerificationProvider
}

// GetSession returns an OCM context and session for the given options.
// It reuses cached contexts/sessions when possible, keyed by configuration and repository hash.
// If not cached, it creates and configures new ones.
func (m *ContextCache) GetSession(
	opts *GetSessionOptions,
) (ocm.Context, ocm.Session, error) {
	// Lazy initialization of verifications using sync.OnceValues.
	verifications := sync.OnceValues(func() ([]Verification, error) {
		if opts.VerificationProvider == nil {
			return nil, nil
		}
		// Load verification info from provider into OCM context.
		return GetVerifications(m.ctx, m.lookupClient, opts.VerificationProvider)
	})

	// Build or retrieve OCM context based on configuration.
	contextHash, newContext, err := ConfigureContext(m.ctx, m.lookupClient, opts.OCMConfigurations, verifications)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to configure context: %w", err)
	}

	var octx ocm.Context
	contextFromCache, ok := m.contexts.Get(contextHash)
	if ok {
		// Reuse cached context, and ensure verifications are registered.
		octx = contextFromCache.(ocm.Context) //nolint:forcetypeassert // safe cast
		verifications, err := verifications()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to configure context: %w", err)
		}
		signInfo := signingattr.Get(octx)
		for _, v := range verifications {
			signInfo.RegisterPublicKey(v.Signature, v.PublicKey)
		}
	} else {
		// Create new context and cache it.
		octx, err = newContext()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to configure context: %w", err)
		}
		m.contexts.Add(contextHash, octx)
		contextCacheSize.WithLabelValues(m.name).Inc()
	}

	// Cache key combines context hash and repository hash.
	type keyType struct {
		ctxHash  string
		repoHash string
	}
	key := keyType{
		ctxHash:  contextHash,
		repoHash: fmt.Sprintf("%x", sha256.Sum256(opts.RepositorySpecification.Raw)),
	}

	var session ocm.Session
	sessionFromCache, ok := m.sessions.Get(key)
	if ok {
		// Reuse cached session if still valid.
		session = sessionFromCache.(ocm.Session) //nolint:forcetypeassert // safe cast
		if session.IsClosed() {
			// Recreate session if it was closed but still in cache.
			session = ocm.NewSession(datacontext.NewSession())
			m.sessions.Add(key, session)
		}
	} else {
		// Create new session, register it with context finalizer, and cache it.
		session = ocm.NewSession(datacontext.NewSession())
		octx.Finalizer().Close(session)
		m.sessions.Add(key, session)
		sessionCacheSize.WithLabelValues(m.name).Inc()
	}

	return octx, session, nil
}

func (m *ContextCache) Clear() {
	m.sessions.Clear()
	m.contexts.Clear()
}
