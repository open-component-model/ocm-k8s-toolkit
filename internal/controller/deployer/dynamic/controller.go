package dynamic

import (
	"context"
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
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

func NewInformerManager(
	mapper meta.RESTMapper,
	register, unregister chan client.Object,
	mapFunc handler.MapFunc,
) (*InformerManager, error) {
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get rest config: %w", err)
	}

	client, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST client: %w", err)
	}

	dynClient, err := dynamic.NewForConfigAndClient(cfg, client)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	return &InformerManager{
		mapper:     mapper,
		clnt:       dynClient,
		register:   register,
		unregister: unregister,
		MapFunc:    mapFunc,
	}, nil
}

var _ source.TypedSource[ctrl.Request] = &InformerManager{}

type InformerManager struct {
	ctx context.Context

	mapper meta.RESTMapper
	clnt   dynamic.Interface

	register   chan client.Object
	unregister chan client.Object

	queue workqueue.TypedRateLimitingInterface[ctrl.Request]
	tasks sync.Map

	handler.MapFunc
}

func (mgr *InformerManager) NeedLeaderElection() bool {
	return true
}

func (mgr *InformerManager) Start(ctx context.Context, queue workqueue.TypedRateLimitingInterface[ctrl.Request]) error {
	mgr.ctx = ctx
	mgr.queue = queue

	logger := ctrl.LoggerFrom(ctx)
	stop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var wg sync.WaitGroup
		defer wg.Wait()

		mgr.tasks.Range(func(key, value any) bool {
			t := value.(*task)
			t.mu.Lock()
			defer t.mu.Unlock()
			go func() {
				defer wg.Done()
				ticker := time.NewTicker(100 * time.Millisecond)
				defer ticker.Stop()

				// lets cancel the task context to stop the informer.
				t.cancel()
				for {
					select {
					case <-ctx.Done():
						logger.Error(ctx.Err(), "failed to wait until task is stopped", "gvr", t.gvr)
						return
					case <-ticker.C:
						if t.informer.Informer().IsStopped() {
							mgr.tasks.Delete(key)
							logger.Info("watch task stopped", "gvr", t.gvr)
						}
					}
				}
			}()
			return true
		})
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				stop()
				return
			case obj := <-mgr.register:
				if err := mgr.Register(obj); err != nil {
					logger.Error(err, "Failed to register object", "object", obj)
				}
			case obj := <-mgr.unregister:
				if err := mgr.Unregister(obj); err != nil {
					logger.Error(err, "Failed to unregister object", "object", obj)
				}
			}
		}
	}()

	return nil
}

type key struct {
	gvr       schema.GroupVersionResource
	namespace string
}

func (mgr *InformerManager) IsStopped(obj runtime.Object) bool {
	if mgr.ctx == nil {
		return true
	}

	task, _, _ := mgr.getTask(obj)
	if task == nil {
		return true
	}

	// Check if the informer is running.
	return task.informer.Informer().IsStopped()
}

func (mgr *InformerManager) HasWatchRegisteredAndIsSynced(obj runtime.Object) bool {
	task, _, _ := mgr.getTask(obj)
	if task == nil {
		return false
	}

	// Check if the informer is running and has synced.
	return task.informer.Informer().HasSynced()
}

func (mgr *InformerManager) getTask(obj runtime.Object) (*task, schema.GroupVersionResource, string) {
	if mgr.ctx == nil {
		return nil, schema.GroupVersionResource{}, ""
	}

	gvk := obj.GetObjectKind().GroupVersionKind()
	mapping, err := mgr.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, schema.GroupVersionResource{}, ""
	}

	gvr := mapping.Resource
	namespace := ""
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		objMeta, err := meta.Accessor(obj)
		if err != nil {
			return nil, gvr, namespace
		}
		namespace = objMeta.GetNamespace()
	}

	taskKey := key{namespace: namespace, gvr: gvr}
	t, ok := mgr.tasks.Load(taskKey)
	if !ok {
		// If no task exists for this GVR and namespace, it means the object is not registered.
		return nil, gvr, namespace
	}

	task := t.(*task)
	return task, gvr, namespace
}

func (mgr *InformerManager) Register(obj runtime.Object) error {
	task, gvr, namespace := mgr.getTask(obj)
	if task != nil {
		return nil
	}
	key := key{namespace: namespace, gvr: gvr}

	task, err := newTask(mgr.clnt, gvr, namespace, cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if m, ok := obj.(client.Object); ok {
				for _, req := range mgr.MapFunc(mgr.ctx, m) {
					// Add the request to the queue for processing.
					mgr.queue.Add(req)
				}
			}
		},
		UpdateFunc: func(_, newObj interface{}) {
			if m, ok := newObj.(client.Object); ok {
				for _, req := range mgr.MapFunc(mgr.ctx, m) {
					// Add the request to the queue for processing.
					mgr.queue.Add(req)
				}
			}
		},
		DeleteFunc: func(obj interface{}) {
			if m, ok := obj.(client.Object); ok {
				for _, req := range mgr.MapFunc(mgr.ctx, m) {
					// Add the request to the queue for processing.
					mgr.queue.Add(req)
				}
			}
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create new task: %w", err)
	}

	if err := task.RunAsync(mgr.ctx); err != nil {
		return fmt.Errorf("failed to run task: %w", err)
	}

	mgr.tasks.Store(key, task)
	return nil
}

func (mgr *InformerManager) Unregister(obj runtime.Object) error {
	if mgr.ctx == nil {
		return fmt.Errorf("InformerManager is not started, call Start() first")
	}

	gvk := obj.GetObjectKind().GroupVersionKind()
	mapping, err := mgr.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("failed to get REST mapping for %s: %w", gvk, err)
	}

	gvr := mapping.Resource
	namespace := ""
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		objMeta, err := meta.Accessor(obj)
		if err != nil {
			return fmt.Errorf("object has no meta: %v", err)
		}
		namespace = objMeta.GetNamespace()
	}

	taskKey := key{namespace: namespace, gvr: gvr}

	t, ok := mgr.tasks.Load(taskKey)
	if !ok {
		return nil
	}

	task := t.(*task)

	if task.ctx.Err() != nil {
		defer mgr.tasks.Delete(taskKey)
		return fmt.Errorf("task for %s is already closed: %w", gvr, task.ctx.Err())
	}

	// lets cancel the task context to stop the informer.
	task.cancel()

	// Wait for the task to stop before removing it from the map.
	// check in this interval if the informer was stopped or until either the task
	// or the manager context is cancelled.
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-mgr.ctx.Done():
			return fmt.Errorf("failed to wait until task is stopped %s: %w", gvr, mgr.ctx.Err())
		case <-ticker.C:
			if task.informer.Informer().IsStopped() {
				mgr.tasks.Delete(taskKey)
				ctrl.LoggerFrom(mgr.ctx).Info("watch task stopped", "gvr", gvr, "namespace", namespace)
				return nil
			}
		}
	}
}

func newTask(
	client dynamic.Interface,
	gvr schema.GroupVersionResource,
	namespace string,
	handler cache.ResourceEventHandler,
) (
	*task, error,
) {
	informer := dynamicinformer.NewFilteredDynamicInformer(
		client,
		gvr,
		namespace,
		60*time.Second,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
		nil,
	)

	return &task{
		informer:     informer,
		eventHandler: handler,
		gvr:          gvr,
	}, nil
}

type task struct {
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex

	gvr schema.GroupVersionResource

	informer     informers.GenericInformer
	eventHandler cache.ResourceEventHandler
	registration cache.ResourceEventHandlerRegistration
}

func (t *task) RunAsync(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.ctx != nil {
		return fmt.Errorf("informer is already running")
	}

	ctx, cancel := context.WithCancel(ctx)
	t.ctx = ctx
	t.cancel = cancel

	registration, err := t.informer.Informer().AddEventHandler(t.eventHandler)
	if err != nil {
		return fmt.Errorf("failed to add event handler to informer: %w", err)
	}
	t.registration = registration

	go t.informer.Informer().RunWithContext(ctx)

	return nil
}
