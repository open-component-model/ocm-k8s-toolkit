package dynamic

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var _ manager.Runnable = &InformerManager{}

type InformerManager struct {
	cache cache.Cache

	register, unregister chan client.Object

	queueMu sync.Mutex // protects the queue from concurrent access
	queue   workqueue.TypedRateLimitingInterface[ctrl.Request]

	tasks sync.Map

	handler handler.EventHandler

	resyncPeriod time.Duration
}

type watchTaskKey struct {
	gvk       schema.GroupVersionKind
	namespace string
}

type watchTask struct {
	informer     cache.Informer
	registration toolscache.ResourceEventHandlerRegistration
}

func NewInformerManager(
	cache cache.Cache,
	handler handler.EventHandler,
) *InformerManager {
	return &InformerManager{
		cache: cache,
		// TODO: consider making the channels buffered
		register:   make(chan client.Object),
		unregister: make(chan client.Object),
		handler:    handler,
	}
}

func (mgr *InformerManager) Source() source.TypedSource[reconcile.Request] {
	return source.Func(func(ctx context.Context, w workqueue.TypedRateLimitingInterface[ctrl.Request]) error {
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
	ctrl.LoggerFrom(ctx).Info("Starting Dynamic Informer Manager")

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		// TODO: add a worker pool here
		return mgr.work(ctx)
	})

	return eg.Wait()
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
			return mgr.stopAllTasks()
		case obj := <-mgr.register:
			if err := mgr.Register(ctx, obj); err != nil {
				logger.Error(err, "register failed", "object", obj)
			}
		case obj := <-mgr.unregister:
			if err := mgr.Unregister(ctx, obj); err != nil {
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
	key := mgr.key(obj)
	if _, ok := mgr.tasks.Load(key); ok {
		return nil // already registered
	}

	inf, err := mgr.cache.GetInformer(ctx, obj)
	if err != nil {
		return fmt.Errorf("failed to get informer for %s: %w", obj.GetName(), err)
	}
	logger := ctrl.LoggerFrom(ctx)
	reg, err := inf.AddEventHandlerWithOptions(toolscache.ResourceEventHandlerDetailedFuncs{
		AddFunc: func(obj any, isInInitialList bool) {
			mgr.handler.Create(ctx, event.TypedCreateEvent[client.Object]{
				Object:          obj.(client.Object),
				IsInInitialList: isInInitialList,
			}, mgr.queue)
		},
		UpdateFunc: func(old, new any) {
			mgr.handler.Update(ctx, event.TypedUpdateEvent[client.Object]{
				ObjectNew: new.(client.Object),
				ObjectOld: old.(client.Object),
			}, mgr.queue)
		},
		DeleteFunc: func(obj any) {
			mgr.handler.Delete(ctx, event.TypedDeleteEvent[client.Object]{
				Object: obj.(client.Object),
			}, mgr.queue)
		},
	}, toolscache.HandlerOptions{
		Logger: &logger,
	})
	if err != nil {
		return fmt.Errorf("failed to add event handler for %s: %w", obj.GetName(), err)
	}

	t := &watchTask{informer: inf, registration: reg}

	mgr.tasks.Store(key, t)
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

	return mgr.stopTask(ctx, key, value.(*watchTask))
}

// --- Private Helpers ---

func (mgr *InformerManager) getTask(obj client.Object) *watchTask {
	key := mgr.key(obj)
	t, ok := mgr.tasks.Load(key)
	if !ok {
		return nil
	}
	return t.(*watchTask)
}

func (mgr *InformerManager) stopAllTasks() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var err error
	mgr.tasks.Range(func(k, v any) bool {
		err = errors.Join(err, mgr.stopTask(ctx, k.(watchTaskKey), v.(*watchTask)))
		return true
	})

	return err
}

func (mgr *InformerManager) stopTask(ctx context.Context, k watchTaskKey, t *watchTask) error {
	ctrl.LoggerFrom(ctx).Info("Stopping dynamic watch task", "gvk", k.gvk, "namespace", k.namespace)
	if err := t.informer.RemoveEventHandler(t.registration); err != nil {
		return fmt.Errorf("failed to remove event handler for %s: %w", k.gvk, err)
	}

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(k.gvk)
	if err := mgr.cache.RemoveInformer(ctx, obj); err != nil {
		return fmt.Errorf("failed to remove informer for %s: %w", k.gvk, err)
	}
	mgr.tasks.Delete(k)
	return nil
}
