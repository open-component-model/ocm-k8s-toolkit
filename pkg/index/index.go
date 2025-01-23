package index

import (
	"context"
	"fmt"
	"maps"

	"github.com/fluxcd/pkg/apis/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

// TargetableAndConfigurableObjectPointerTo is a type that is used to define a pointer to a regular client object.
// However it also contains references to set/get configuration and target of a resource.
// This is used together with TargetAndConfig to index all fields for the target and config,
// and to build up handler.EventHandler's that automatically trigger an update on the given object
// whenever either the configuration or the target changes.
type TargetableAndConfigurableObjectPointerTo[T any] interface {
	*T
	client.Object
	GetConfig() *v1alpha1.ConfigurationReference
	SetConfig(ref *v1alpha1.ConfigurationReference)
	GetTarget() *v1alpha1.ConfigurationReference
	SetTarget(ref *v1alpha1.ConfigurationReference)
}

// TargetAndConfig returns two handler.EventHandler that can be used to trigger an update on the given object
// if the corresponding configuration or target changes.
// It opinionates on the fields spec because client-side field specs for indexing do not need to be canonical.
// It uses this specification to generate field matching functions via ReferenceIndex on the v1alpha1.ConfigurationReference.
// This is then used together with EnqueueForFieldMatcher to generate the handler.EventHandler's.
// This means that everytime an object that implements TargetableAndConfigurableObjectPointerTo[T] is having either
// its Config or Target changed, the event handler will be able to pick up this change as long as
// it is registered in a SetupWithManager call.
func TargetAndConfig[T any, P TargetableAndConfigurableObjectPointerTo[T]](mgr ctrl.Manager) (
	handler.EventHandler,
	handler.EventHandler,
	error,
) {
	// Index all fields for the target and config, and build up MatchingFieldsFunc.
	configFields, configIndex := ReferenceIndex("spec.config", func(obj P) *v1alpha1.ConfigurationReference {
		return obj.GetConfig()
	})
	targetFields, targetIndex := ReferenceIndex("spec.target", func(obj P) *v1alpha1.ConfigurationReference {
		return obj.GetTarget()
	})

	mappings := map[string]func(obj P) []string{}
	maps.Copy(mappings, configIndex)
	maps.Copy(mappings, targetIndex)

	// Index all fields accordingly
	for field, mapping := range mappings {
		if err := mgr.GetFieldIndexer().IndexField(context.Background(), P(new(T)), field, func(obj client.Object) []string {
			resource, ok := obj.(P)
			if !ok {
				return nil
			}

			// Make sure that the target gets indexed / defaulted with the correct GVK in case not all fields were set
			trgtRef := resource.GetTarget()
			if err := DefaultGVKFromScheme(&trgtRef.NamespacedObjectKindReference, mgr.GetScheme(), v1alpha1.GroupVersion); err == nil {
				resource.SetTarget(trgtRef)
			}

			// Make sure that the config gets indexed / defaulted with the correct GVK in case not all fields were set
			cfgRef := resource.GetConfig()
			if err := DefaultGVKFromScheme(&cfgRef.NamespacedObjectKindReference, mgr.GetScheme(), v1alpha1.GroupVersion); err == nil {
				resource.SetConfig(cfgRef)
			}

			return mapping(resource)
		}); err != nil {
			return nil, nil, err
		}
	}

	return EnqueueForFieldMatcher(mgr.GetClient(), targetFields), EnqueueForFieldMatcher(mgr.GetClient(), configFields), nil
}

func DefaultGVKFromScheme(ref *meta.NamespacedObjectKindReference, scheme *runtime.Scheme, defaultGroupVersion schema.GroupVersion) error {
	if ref.APIVersion == "" {
		ref.APIVersion = defaultGroupVersion.String()
	}
	obj := v1.PartialObjectMetadata{}
	obj.SetGroupVersionKind(schema.FromAPIVersionAndKind(ref.APIVersion, ref.Kind))
	gvk, err := apiutil.GVKForObject(&obj, scheme)
	if err != nil {
		return err
	}
	apiVersion, kind := gvk.ToAPIVersionAndKind()
	ref.APIVersion = apiVersion
	ref.Kind = kind

	return nil
}

func ReferenceIndex[T any](fieldSpec string, refFunc func(obj T) *v1alpha1.ConfigurationReference) (
	func(object client.Object) client.MatchingFields,
	map[string]func(obj T) []string,
) {
	return MatchingFieldsFuncForNamespacedObjectKind(fieldSpec), ConfigurationReferenceMappings[T](
		fieldSpec,
		refFunc,
	)
}

func MatchingFieldsFuncForNamespacedObjectKind(prefix string) func(object client.Object) client.MatchingFields {
	return func(object client.Object) client.MatchingFields {
		apiVersion, kind := object.GetObjectKind().GroupVersionKind().ToAPIVersionAndKind()

		return client.MatchingFields{
			fmt.Sprintf("%s.name", prefix):       object.GetName(),
			fmt.Sprintf("%s.namespace", prefix):  object.GetNamespace(),
			fmt.Sprintf("%s.kind", prefix):       kind,
			fmt.Sprintf("%s.apiVersion", prefix): apiVersion,
		}
	}
}

func ConfigurationReferenceMappings[T any](
	pathSpec string,
	refFunc func(obj T) *v1alpha1.ConfigurationReference,
) map[string]func(T) []string {
	return map[string]func(obj T) []string{
		fmt.Sprintf("%s.name", pathSpec):       func(obj T) []string { return []string{refFunc(obj).Name} },
		fmt.Sprintf("%s.namespace", pathSpec):  func(obj T) []string { return []string{refFunc(obj).Namespace} },
		fmt.Sprintf("%s.kind", pathSpec):       func(obj T) []string { return []string{refFunc(obj).Kind} },
		fmt.Sprintf("%s.apiVersion", pathSpec): func(obj T) []string { return []string{refFunc(obj).APIVersion} },
	}
}

// EnqueueForFieldMatcher returns an event handler that enqueues requests for the given object if the fields match.
// The fields function should return the fields to match on for the given object.
func EnqueueForFieldMatcher(clnt client.Client, fields func(obj client.Object) client.MatchingFields) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
		gvk, err := apiutil.GVKForObject(object, clnt.Scheme())
		if err != nil {
			return nil
		}
		object.GetObjectKind().SetGroupVersionKind(gvk)
		list := &v1alpha1.LocalizedResourceList{}
		if err := clnt.List(ctx, list, fields(object)); err != nil {
			return nil
		}
		requests := make([]reconcile.Request, 0, len(list.Items))
		for _, item := range list.Items {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&item)})
		}

		return requests
	})
}
