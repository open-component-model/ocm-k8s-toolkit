package dynamic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	toolscache "k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
)

const DefaultShutdownTimeout = 30 * time.Second

var _ manager.Runnable = &InformerManager{}

type InformerManager struct {
	workers int
	cache   cache.Cache

	register, unregister chan client.Object

	queueMu sync.RWMutex // protects the queue from concurrent access
	queue   workqueue.TypedRateLimitingInterface[ctrl.Request]

	tasks sync.Map

	handler handler.EventHandler

	metricsLabel    string
	shutdownTimeout time.Duration
}

type watchTaskKey struct {
	gvk       schema.GroupVersionKind
	namespace string
}

type watchTask struct {
	informer     cache.Informer
	registration toolscache.ResourceEventHandlerRegistration
	active       atomic.Int32
}

type Options struct {
	Config     *rest.Config
	HTTPClient *http.Client
	RESTMapper meta.RESTMapper // if nil, a dynamic REST mapper will be created

	Handler handler.EventHandler

	DefaultLabelSelector labels.Selector

	Workers int

	RegisterChannelBufferSize   int
	UnregisterChannelBufferSize int

	ShutdownTimeout time.Duration

	MetricsLabel string
}

func NewInformerManager(opts *Options) (*InformerManager, error) {
	mapper := opts.RESTMapper
	if mapper == nil {
		var err error
		if mapper, err = apiutil.NewDynamicRESTMapper(opts.Config, opts.HTTPClient); err != nil {
			return nil, fmt.Errorf("failed to create REST mapper: %w", err)
		}
	}

	// Here we store the dynamic data in a cache.
	// Note that we do not pass a scheme here because we only work with partial metadata
	metadataCache, err := cache.New(opts.Config, cache.Options{
		HTTPClient:                   opts.HTTPClient,
		Mapper:                       mapper,
		ReaderFailOnMissingInformer:  true,
		DefaultLabelSelector:         opts.DefaultLabelSelector,
		DefaultTransform:             TransformPartialObjectMetadata,
		DefaultUnsafeDisableDeepCopy: ptr.To(true),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create cache: %w", err)
	}

	var workers int
	if opts.Workers > 0 {
		workers = opts.Workers
	} else {
		workers = 1 // default to 1 worker if not specified
	}

	shutdownTimeout := opts.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = DefaultShutdownTimeout
	}

	mgr := &InformerManager{
		cache:           metadataCache,
		register:        make(chan client.Object, opts.RegisterChannelBufferSize),
		unregister:      make(chan client.Object, opts.UnregisterChannelBufferSize),
		handler:         opts.Handler,
		workers:         workers,
		metricsLabel:    opts.MetricsLabel,
		shutdownTimeout: shutdownTimeout,
	}

	return mgr, nil
}

func (mgr *InformerManager) Source() source.TypedSource[reconcile.Request] {
	return source.Func(func(_ context.Context, w workqueue.TypedRateLimitingInterface[ctrl.Request]) error {
		// this dynamically binds the given queue to the informer manager
		// this means that from this point on, the queue will receive events for all registered watches
		mgr.queueMu.Lock()
		defer mgr.queueMu.Unlock()
		if mgr.queue != nil {
			return fmt.Errorf("another queue is already registered with the informer manager")
		}
		mgr.queue = w

		return nil
	})
}

func (mgr *InformerManager) NeedLeaderElection() bool {
	// this manager does need leader election, as it is designed to run in a single instance
	// this is to ensure that the dynamic informers are not started multiple times across different controller instances
	return true
}

func (mgr *InformerManager) Start(ctx context.Context) error {
	logger := ctrl.LoggerFrom(ctx).WithValues("name", mgr.metricsLabel)
	logger.Info("Starting Dynamic Informer Manager")

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		// start the cache that holds our dynamic informer states.
		// note that we do not need to wait for a sync here because we only register dynamic informers
		// and the cache will not have any initial data.
		return mgr.cache.Start(ctx)
	})

	for range mgr.workers {
		eg.Go(func() error {
			return mgr.work(ctx)
		})
	}

	// Shutdown logic
	if err := eg.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	logger.Info("Shutting down dynamic informer manager", "timeout", mgr.shutdownTimeout)

	//nolint:contextcheck // we are using context.Background() here because after the shutdown we don't have the
	// origin context anymore.
	if err := mgr.GracefulShutdown(context.Background(), mgr.shutdownTimeout); err != nil {
		return fmt.Errorf("failed to gracefully shutdown informer manager: %w", err)
	}

	return nil
}

// --- Public State Helpers ---

func (mgr *InformerManager) IsStopped(obj client.Object) bool {
	task := mgr.getTask(obj)

	return task == nil || task.informer.IsStopped()
}

func (mgr *InformerManager) HasSynced(obj client.Object) bool {
	task := mgr.getTask(obj)

	return task != nil && task.informer.HasSynced() && task.registration.HasSynced()
}

// --- Register / Unregister ---

func (mgr *InformerManager) work(ctx context.Context) error {
	logger := ctrl.LoggerFrom(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case obj := <-mgr.register:
			timer := prometheus.NewTimer(workerOperationDuration.WithLabelValues(mgr.metricsLabel, "register"))
			err := mgr.Register(ctx, obj)
			timer.ObserveDuration()
			if err != nil {
				logger.Error(err, "register failed", "object", obj)
			}
		case obj := <-mgr.unregister:
			timer := prometheus.NewTimer(workerOperationDuration.WithLabelValues(mgr.metricsLabel, "unregister"))
			err := mgr.Unregister(ctx, obj)
			timer.ObserveDuration()
			if err != nil {
				logger.Error(err, "unregister failed", "object", obj)
			}
		}
	}
}

func (mgr *InformerManager) RegisterChannel() chan client.Object {
	return mgr.register
}

func (mgr *InformerManager) UnregisterChannel() chan client.Object {
	return mgr.unregister
}

func (mgr *InformerManager) Register(ctx context.Context, obj client.Object) error {
	logger := ctrl.LoggerFrom(ctx)

	key := mgr.key(obj)
	if t, ok := mgr.tasks.Load(key); ok {
		logger.Info("Incrementing active count for already running dynamic watch task", "gvk", key.gvk, "namespace", key.namespace)
		t.(*watchTask).active.Add(1) //nolint:forcetypeassert // we know the type is a task

		return nil // already registered
	}

	inf, err := mgr.cache.GetInformer(ctx, obj)
	if err != nil {
		return fmt.Errorf("failed to get informer for %s: %w", obj.GetName(), err)
	}

	withQueue := func(f func(queue workqueue.TypedRateLimitingInterface[ctrl.Request])) {
		mgr.queueMu.RLock()
		defer mgr.queueMu.RUnlock()
		if mgr.queue == nil {
			logger.Error(fmt.Errorf("queue is not set"), "cannot process event", "object", obj)

			return
		}
		f(mgr.queue)
	}

	eventHandler := toolscache.ResourceEventHandlerDetailedFuncs{
		AddFunc: func(o any, isInit bool) {
			withQueue(func(queue workqueue.TypedRateLimitingInterface[ctrl.Request]) {
				eventCount.WithLabelValues(mgr.metricsLabel, "add").Inc()
				mgr.handler.Create(ctx, event.TypedCreateEvent[client.Object]{
					Object:          o.(client.Object), //nolint:forcetypeassert // we know the type is client.Object
					IsInInitialList: isInit,
				}, queue)
			})
		},
		UpdateFunc: func(oldObject, newObject any) {
			withQueue(func(queue workqueue.TypedRateLimitingInterface[ctrl.Request]) {
				eventCount.WithLabelValues(mgr.metricsLabel, "update").Inc()
				mgr.handler.Update(ctx, event.TypedUpdateEvent[client.Object]{
					ObjectNew: newObject.(client.Object), //nolint:forcetypeassert // we know the type is client.Object
					//nolint:forcetypeassert // we know the type is client.Object
					ObjectOld: oldObject.(client.Object),
				}, queue)
			})
		},
		DeleteFunc: func(o any) {
			withQueue(func(queue workqueue.TypedRateLimitingInterface[ctrl.Request]) {
				eventCount.WithLabelValues(mgr.metricsLabel, "delete").Inc()
				mgr.handler.Delete(ctx, event.TypedDeleteEvent[client.Object]{
					Object: o.(client.Object), //nolint:forcetypeassert // we know the type is client.Object
				}, queue)
			})
		},
	}

	reg, err := inf.AddEventHandlerWithOptions(eventHandler, toolscache.HandlerOptions{Logger: &logger})
	if err != nil {
		return fmt.Errorf("failed to add event handler for %s: %w", obj.GetName(), err)
	}

	t := &watchTask{informer: inf, registration: reg}
	t.active.Store(1) // start with 1 active count
	mgr.tasks.Store(key, t)
	activeTasks.WithLabelValues(mgr.metricsLabel).Inc()
	registerTotal.WithLabelValues(mgr.metricsLabel, key.gvk.Group, key.gvk.Version, key.gvk.Kind, key.namespace).Inc()

	return nil
}

func (mgr *InformerManager) key(obj client.Object) watchTaskKey {
	return watchTaskKey{obj.GetObjectKind().GroupVersionKind(), obj.GetNamespace()}
}

func (mgr *InformerManager) Unregister(ctx context.Context, obj client.Object) error {
	key := mgr.key(obj)

	value, ok := mgr.tasks.Load(key)
	if !ok {
		return nil
	}

	if err := mgr.stopTask(ctx, key, value.(*watchTask)); err != nil { //nolint:forcetypeassert // we know the type
		return fmt.Errorf("failed to stop task for %s: %w", obj.GetName(), err)
	}

	unregisterTotal.WithLabelValues(mgr.metricsLabel, key.gvk.Group, key.gvk.Version, key.gvk.Kind, key.namespace).Inc()

	return nil
}

// --- Private Helpers ---

func (mgr *InformerManager) getTask(obj client.Object) *watchTask {
	key := mgr.key(obj)
	t, ok := mgr.tasks.Load(key)
	if !ok {
		return nil
	}

	task, _ := t.(*watchTask)

	return task
}

func (mgr *InformerManager) GracefulShutdown(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var err error
	mgr.tasks.Range(func(k, v any) bool {
		err = errors.Join(err, mgr.stopTask(ctx, k.(watchTaskKey), v.(*watchTask))) //nolint:forcetypeassert // we know the type

		return true
	})

	mgr.queueMu.Lock()
	defer mgr.queueMu.Unlock()
	// dereference the queue to stop processing events
	// the queue lifecycle is managed by the manager, so we don't own its shutdown
	mgr.queue = nil

	close(mgr.register)
	close(mgr.unregister)

	return err
}

func (mgr *InformerManager) stopTask(ctx context.Context, k watchTaskKey, t *watchTask) error {
	logger := ctrl.LoggerFrom(ctx).WithValues("gvk", k.gvk, "namespace", k.namespace)

	if t.active.Load() > 1 {
		logger.Info("Decrementing active count for dynamic watch task")
		t.active.Add(-1)

		return nil
	}
	logger.Info("Stopping dynamic watch task")

	if err := t.informer.RemoveEventHandler(t.registration); err != nil {
		return fmt.Errorf("failed to remove event handler for %s: %w", k.gvk, err)
	}

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(k.gvk)
	if err := mgr.cache.RemoveInformer(ctx, obj); err != nil {
		return fmt.Errorf("failed to remove informer for %s: %w", k.gvk, err)
	}
	mgr.tasks.Delete(k)
	activeTasks.WithLabelValues(mgr.metricsLabel).Dec()

	return nil
}
