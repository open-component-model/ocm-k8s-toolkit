package localization

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

func indexTargetAndConfig(mgr ctrl.Manager) (
	func(object client.Object) client.MatchingFields,
	func(object client.Object) client.MatchingFields,
	error,
) {
	configFields, configIndex := ConfigIndex()
	targetFields, targetIndex := TargetIndex()

	mappings := map[string]func(obj *v1alpha1.LocalizedResource) []string{}
	maps.Copy(mappings, configIndex)
	maps.Copy(mappings, targetIndex)

	// Index all fields accordingly
	for field, mapping := range mappings {
		if err := mgr.GetFieldIndexer().IndexField(context.Background(), &v1alpha1.LocalizedResource{}, field, func(obj client.Object) []string {
			resource, ok := obj.(*v1alpha1.LocalizedResource)
			if !ok {
				return nil
			}

			trgtRef := resource.Spec.Target
			if err := DefaultGVKFromScheme(&trgtRef.NamespacedObjectKindReference, mgr.GetScheme()); err == nil {
				trgtRef.DeepCopyInto(&resource.Spec.Target)
			}

			cfgRef := resource.Spec.Config
			if err := DefaultGVKFromScheme(&cfgRef.NamespacedObjectKindReference, mgr.GetScheme()); err == nil {
				cfgRef.DeepCopyInto(&resource.Spec.Config)
			}

			return mapping(resource)
		}); err != nil {
			return nil, nil, err
		}
	}

	return targetFields, configFields, nil
}

func DefaultGVKFromScheme(ref *meta.NamespacedObjectKindReference, scheme *runtime.Scheme) error {
	if ref.APIVersion == "" {
		ref.APIVersion = v1alpha1.GroupVersion.String()
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

func TargetIndex() (
	func(object client.Object) client.MatchingFields,
	map[string]func(obj *v1alpha1.LocalizedResource) []string,
) {
	// Create index fields for the target (the data that is to be localized)
	fieldsInTarget := createFields("spec.target")

	targetMappings := createMappings("spec.target", func(obj *v1alpha1.LocalizedResource) *v1alpha1.ConfigurationReference {
		return &obj.Spec.Target
	})

	return fieldsInTarget, targetMappings
}

func ConfigIndex() (
	func(object client.Object) client.MatchingFields,
	map[string]func(obj *v1alpha1.LocalizedResource) []string,
) {
	// Create index fields for the configuration (the data that is used to localize the target)
	fieldsInConfig := createFields("spec.config")

	configMappings := createMappings("spec.config", func(obj *v1alpha1.LocalizedResource) *v1alpha1.ConfigurationReference {
		return &obj.Spec.Config
	})

	return fieldsInConfig, configMappings
}

func createFields(prefix string) func(object client.Object) client.MatchingFields {
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

func createMappings(prefix string, refFunc func(obj *v1alpha1.LocalizedResource) *v1alpha1.ConfigurationReference) map[string]func(obj *v1alpha1.LocalizedResource) []string {
	return map[string]func(obj *v1alpha1.LocalizedResource) []string{
		fmt.Sprintf("%s.name", prefix):       func(obj *v1alpha1.LocalizedResource) []string { return []string{refFunc(obj).Name} },
		fmt.Sprintf("%s.namespace", prefix):  func(obj *v1alpha1.LocalizedResource) []string { return []string{refFunc(obj).Namespace} },
		fmt.Sprintf("%s.kind", prefix):       func(obj *v1alpha1.LocalizedResource) []string { return []string{refFunc(obj).Kind} },
		fmt.Sprintf("%s.apiVersion", prefix): func(obj *v1alpha1.LocalizedResource) []string { return []string{refFunc(obj).APIVersion} },
	}
}

// enqueueForFieldMatcher returns an event handler that enqueues requests for the given object if the fields match.
// The fields function should return the fields to match on for the given object.
func enqueueForFieldMatcher(clnt client.Client, fields func(obj client.Object) client.MatchingFields) handler.EventHandler {
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
