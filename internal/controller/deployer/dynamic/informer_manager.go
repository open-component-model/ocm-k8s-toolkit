package dynamic

import (
	"context"
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var _ source.TypedSource[ctrl.Request] = &InformerManager{}

type InformerManager struct {
	ctx    context.Context
	mapper meta.RESTMapper
	clnt   dynamic.Interface

	register, unregister chan client.Object
	queue                workqueue.TypedRateLimitingInterface[ctrl.Request]
	tasks                sync.Map

	handler.MapFunc

	tweakListOptions dynamicinformer.TweakListOptionsFunc
}

type key struct {
	gvr       schema.GroupVersionResource
	namespace string
}

type task struct {
	ctx          context.Context
	cancel       context.CancelFunc
	mu           sync.Mutex
	gvr          schema.GroupVersionResource
	informer     informers.GenericInformer
	eventHandler cache.ResourceEventHandler
}

func NewInformerManager(
	mapper meta.RESTMapper,
	mapFunc handler.MapFunc,
	tweakListOptions dynamicinformer.TweakListOptionsFunc,
) (*InformerManager, error) {
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get rest config: %w", err)
	}

	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST client: %w", err)
	}

	dynClient, err := dynamic.NewForConfigAndClient(cfg, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return &InformerManager{
		mapper:           mapper,
		clnt:             dynClient,
		register:         make(chan client.Object),
		unregister:       make(chan client.Object),
		MapFunc:          mapFunc,
		tweakListOptions: tweakListOptions,
	}, nil
}

func (mgr *InformerManager) Start(ctx context.Context, queue workqueue.TypedRateLimitingInterface[ctrl.Request]) error {
	mgr.ctx = ctx
	mgr.queue = queue

	go mgr.registerWorker(ctx)
	return nil
}

// --- Public State Helpers ---

func (mgr *InformerManager) IsStopped(obj runtime.Object) bool {
	task, _, _ := mgr.getTask(obj)
	return task == nil || task.informer.Informer().IsStopped()
}

func (mgr *InformerManager) HasWatchRegisteredAndIsSynced(obj runtime.Object) bool {
	task, _, _ := mgr.getTask(obj)
	return task != nil && task.informer.Informer().HasSynced()
}

// --- Register / Unregister ---

func (mgr *InformerManager) registerWorker(ctx context.Context) {
	logger := ctrl.LoggerFrom(ctx)
	for {
		select {
		case <-ctx.Done():
			mgr.stopAllTasks()
			return
		case obj := <-mgr.register:
			if err := mgr.Register(obj); err != nil {
				logger.Error(err, "register failed", "object", obj)
			}
		case obj := <-mgr.unregister:
			if err := mgr.Unregister(obj); err != nil {
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

func (mgr *InformerManager) Register(obj runtime.Object) error {
	_, gvr, ns, err := mgr.resolveKey(obj)
	if err != nil {
		return err
	}

	if _, ok := mgr.tasks.Load(key{gvr, ns}); ok {
		return nil // already registered
	}

	inf := dynamicinformer.NewFilteredDynamicInformer(
		mgr.clnt, gvr, ns, 60*time.Second,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, mgr.tweakListOptions,
	)
	t := &task{gvr: gvr, informer: inf, eventHandler: mgr.eventHandler()}

	if err := t.RunAsync(mgr.ctx); err != nil {
		return err
	}

	mgr.tasks.Store(key{gvr, ns}, t)
	return nil
}

func (mgr *InformerManager) Unregister(obj runtime.Object) error {
	k, _, _, err := mgr.resolveKey(obj)
	if err != nil {
		return err
	}

	value, ok := mgr.tasks.Load(k)
	if !ok {
		return nil
	}
	return mgr.stopTask(k, value.(*task))
}

// --- Private Helpers ---

func (mgr *InformerManager) getTask(obj runtime.Object) (*task, schema.GroupVersionResource, string) {
	k, gvr, ns, err := mgr.resolveKey(obj)
	if err != nil {
		return nil, gvr, ns
	}

	t, ok := mgr.tasks.Load(k)
	if !ok {
		return nil, gvr, ns
	}
	return t.(*task), gvr, ns
}

func (mgr *InformerManager) resolveKey(obj runtime.Object) (key, schema.GroupVersionResource, string, error) {
	if mgr.ctx == nil {
		return key{}, schema.GroupVersionResource{}, "", fmt.Errorf("manager not started")
	}

	gvk := obj.GetObjectKind().GroupVersionKind()
	mapping, err := mgr.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return key{}, schema.GroupVersionResource{}, "", err
	}

	ns := ""
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		metaObj, err := meta.Accessor(obj)
		if err != nil {
			return key{}, mapping.Resource, "", err
		}
		ns = metaObj.GetNamespace()
	}
	return key{mapping.Resource, ns}, mapping.Resource, ns, nil
}

func (mgr *InformerManager) stopAllTasks() {
	var wg sync.WaitGroup
	mgr.tasks.Range(func(k, v any) bool {
		wg.Add(1)
		go func(k key, t *task) {
			defer wg.Done()
			_ = mgr.stopTask(k, t)
		}(k.(key), v.(*task))
		return true
	})
	wg.Wait()
}

func (mgr *InformerManager) stopTask(k key, t *task) error {
	t.cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-mgr.ctx.Done():
			return mgr.ctx.Err()
		case <-ticker.C:
			if t.informer.Informer().IsStopped() {
				mgr.tasks.Delete(k)
				return nil
			}
		}
	}
}

func (mgr *InformerManager) eventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    mgr.enqueue,
		UpdateFunc: func(_, newObj interface{}) { mgr.enqueue(newObj) },
		DeleteFunc: mgr.enqueue,
	}
}

func (mgr *InformerManager) enqueue(obj interface{}) {
	if m, ok := obj.(client.Object); ok {
		for _, req := range mgr.MapFunc(mgr.ctx, m) {
			mgr.queue.Add(req)
		}
	}
}

func (t *task) RunAsync(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.ctx != nil {
		return fmt.Errorf("informer already running")
	}

	t.ctx, t.cancel = context.WithCancel(ctx)
	if _, err := t.informer.Informer().AddEventHandler(t.eventHandler); err != nil {
		return err
	}
	if err := t.informer.Informer().SetTransform(partialObjectMetadataFromUnstructured); err != nil {
		return fmt.Errorf("failed to set transform: %w", err)
	}

	go t.informer.Informer().RunWithContext(t.ctx)
	return nil
}

var allowedFieldsForPartialObjectMetadata = map[string]struct{}{
	"apiVersion": {},
	"kind":       {},
	"metadata":   {},
}

// partialObjectMetaDataFromUnstructured returns a transform function that
// strips all fields from an unstructured object except apiVersion, kind, and metadata.
func partialObjectMetadataFromUnstructured(obj any) (any, error) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T, expected *unstructured.Unstructured", obj)
	}
	for key := range u.Object {
		if _, allowed := allowedFieldsForPartialObjectMetadata[key]; !allowed {
			delete(u.Object, key)
		}
	}
	return u, nil
}
