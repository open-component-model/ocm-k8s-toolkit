package index

import (
	"context"
	"fmt"
	"maps"

	"github.com/fluxcd/pkg/apis/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

func LocalizedResourceIndexTargetAndConfig(mgr ctrl.Manager) (
	func(object client.Object) client.MatchingFields,
	func(object client.Object) client.MatchingFields,
	error,
) {
	// Index all fields for the target and config, and build up MatchingFieldsFunc.
	configFields, configIndex := ReferenceIndex("spec.config", func(obj client.Object) *v1alpha1.ConfigurationReference {
		return &obj.(*v1alpha1.LocalizedResource).Spec.Config //nolint:forcetypeassert // We know it's ok
	})
	targetFields, targetIndex := ReferenceIndex("spec.target", func(obj client.Object) *v1alpha1.ConfigurationReference {
		return &obj.(*v1alpha1.LocalizedResource).Spec.Target //nolint:forcetypeassert // We know it's ok
	})

	mappings := map[string]func(obj client.Object) []string{}
	maps.Copy(mappings, configIndex)
	maps.Copy(mappings, targetIndex)

	// Index all fields accordingly
	for field, mapping := range mappings {
		if err := mgr.GetFieldIndexer().IndexField(context.Background(), &v1alpha1.LocalizedResource{}, field, func(obj client.Object) []string {
			resource, ok := obj.(*v1alpha1.LocalizedResource)
			if !ok {
				return nil
			}

			// Make sure that the target gets indexed / defaulted with the correct GVK in case not all fields were set
			trgtRef := resource.Spec.Target
			if err := DefaultGVKFromScheme(&trgtRef.NamespacedObjectKindReference, mgr.GetScheme(), v1alpha1.GroupVersion); err == nil {
				trgtRef.DeepCopyInto(&resource.Spec.Target)
			}

			// Make sure that the config gets indexed / defaulted with the correct GVK in case not all fields were set
			cfgRef := resource.Spec.Config
			if err := DefaultGVKFromScheme(&cfgRef.NamespacedObjectKindReference, mgr.GetScheme(), v1alpha1.GroupVersion); err == nil {
				cfgRef.DeepCopyInto(&resource.Spec.Config)
			}

			return mapping(resource)
		}); err != nil {
			return nil, nil, err
		}
	}

	return targetFields, configFields, nil
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

func ReferenceIndex(fieldSpec string, refFunc func(obj client.Object) *v1alpha1.ConfigurationReference) (
	func(object client.Object) client.MatchingFields,
	map[string]func(obj client.Object) []string,
) {
	return MatchingFieldsFuncForNamespacedObjectKind(fieldSpec), ConfigurationReferenceMappings(
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

func ConfigurationReferenceMappings(
	pathSpec string,
	refFunc func(obj client.Object) *v1alpha1.ConfigurationReference,
) map[string]func(obj client.Object) []string {
	return map[string]func(obj client.Object) []string{
		fmt.Sprintf("%s.name", pathSpec):       func(obj client.Object) []string { return []string{refFunc(obj).Name} },
		fmt.Sprintf("%s.namespace", pathSpec):  func(obj client.Object) []string { return []string{refFunc(obj).Namespace} },
		fmt.Sprintf("%s.kind", pathSpec):       func(obj client.Object) []string { return []string{refFunc(obj).Kind} },
		fmt.Sprintf("%s.apiVersion", pathSpec): func(obj client.Object) []string { return []string{refFunc(obj).APIVersion} },
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
