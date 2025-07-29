package dynamic

import (
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type key struct {
	gvk       schema.GroupVersionKind
	namespace string
	name      string
}

type entry struct {
	child sync.Map // map[key]client.Object
}

type Tracker struct {
	byUID sync.Map // map[parentUID]*entry
}

func NewTracker() *Tracker {
	return &Tracker{}
}

func (t *Tracker) Track(parent, obj client.Object) error {
	p, key, child, err := prepareKeys(parent, obj)
	if err != nil {
		return fmt.Errorf("track: %w", err)
	}

	entry := t.getOrCreateEntry(p.GetUID())
	entry.child.Store(key, child)

	return nil
}

func (t *Tracker) Untrack(parent, obj client.Object) error {
	p, key, _, err := prepareKeys(parent, obj)
	if err != nil {
		return fmt.Errorf("untrack: %w", err)
	}

	entryRaw, ok := t.byUID.Load(p.GetUID())
	if !ok {
		return nil
	}
	entry := entryRaw.(*entry) //nolint:forcetypeassert // we know the type
	entry.child.Delete(key)

	// Delete parent if empty
	if isEmpty(&entry.child) {
		t.byUID.Delete(p.GetUID())
	}

	return nil
}

func (t *Tracker) GetTracked(parent client.Object) ([]client.Object, error) {
	p, err := trim(parent)
	if err != nil {
		return nil, fmt.Errorf("getTracked: %w", err)
	}

	entryRaw, ok := t.byUID.Load(p.GetUID())
	if !ok {
		return nil, nil
	}

	entry := entryRaw.(*entry) //nolint:forcetypeassert // we know the type
	var objects []client.Object
	entry.child.Range(func(_, v any) bool {
		objects = append(objects, v.(client.Object)) //nolint:forcetypeassert // we know the type

		return true
	})

	return objects, nil
}

// ---- helpers ----

func (t *Tracker) getOrCreateEntry(uid any) *entry {
	entryIface, _ := t.byUID.LoadOrStore(uid, &entry{})

	return entryIface.(*entry) //nolint:forcetypeassert // we know the type
}

func isEmpty(m *sync.Map) bool {
	empty := true
	m.Range(func(_, _ any) bool {
		empty = false

		return false
	})

	return empty
}

func prepareKeys(parent, obj client.Object) (*v1.PartialObjectMetadata, key, client.Object, error) {
	p, err := trim(parent)
	if err != nil {
		return nil, key{}, nil, err
	}
	c, err := trim(obj)
	if err != nil {
		return nil, key{}, nil, err
	}
	key := key{
		gvk:       obj.GetObjectKind().GroupVersionKind(),
		namespace: c.GetNamespace(),
		name:      c.GetName(),
	}

	return p, key, obj, nil
}

func trim(obj runtime.Object) (*v1.PartialObjectMetadata, error) {
	if m, err := TransformPartialObjectMetadata(obj); err == nil {
		if partial, ok := m.(*v1.PartialObjectMetadata); ok {
			return partial, nil
		}
	}

	return nil, fmt.Errorf("trim: expected PartialObjectMetadata, got %T", obj)
}
