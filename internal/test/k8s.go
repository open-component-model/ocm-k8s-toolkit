package test

import (
	"context"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2" //nolint:revive // linter is not aware of ginkgo
	. "github.com/onsi/gomega"    //nolint:revive // linter is not aware of ginkgo

	"github.com/fluxcd/pkg/runtime/conditions"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/internal/util"
)

func SanitizeNameForK8s(name string) string {
	replaced := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	maxLength := 63 // RFC 1123 Label Names
	if len(replaced) > maxLength {
		return replaced[:maxLength]
	}

	return replaced
}

// WaitForReadyObject waits for a Kubernetes object to reach a ready state.
//
// Parameters:
// - ctx: The context for managing request deadlines and cancellations.
// - k8sClient: The Kubernetes client used to interact with the cluster.
// - obj: The Kubernetes object to monitor, implementing the util.Getter interface.
// - waitForField: A map specifying field-value pairs to validate on the object.
//
// Behavior:
// - Periodically checks if the object exists, is not being deleted, and is in a ready state.
// - Verifies that the specified fields in waitForField match the expected values.
// - Fails the test if the object does not meet the conditions within 15 seconds.
func WaitForReadyObject(ctx context.Context, k8sClient client.Client, obj util.Getter, waitForField map[string]any) {
	GinkgoHelper()

	Eventually(func(g Gomega, ctx context.Context) error {
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj)
		if err != nil {
			if errors.IsNotFound(err) {
				return fmt.Errorf("resource %s (Kind: %s) not found", obj.GetName(), obj.GetObjectKind())
			}

			return err
		}

		if obj.GetDeletionTimestamp() != nil {
			return fmt.Errorf("resource %s (Kind: %s) is being deleted", obj.GetName(), obj.GetObjectKind())
		}

		if !conditions.IsReady(obj) {
			return fmt.Errorf("resource %s (Kind: %s) not ready", obj.GetName(), obj.GetObjectKind())
		}

		for field, value := range waitForField {
			g.Expect(obj).Should(HaveField(field, value))
		}

		return nil
	}, "15s").WithContext(ctx).Should(Succeed())
}

func DeleteObject(ctx context.Context, k8sClient client.Client, obj client.Object) {
	GinkgoHelper()

	Expect(k8sClient.Delete(ctx, obj)).To(Succeed())

	Eventually(func(ctx context.Context) error {
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj)
		if err != nil {
			if errors.IsNotFound(err) {
				return nil
			}

			return err
		}

		return fmt.Errorf("resource %s (Kind: %s) still exists", obj.GetName(), obj.GetObjectKind())
	}, "15s").WithContext(ctx).Should(Succeed())
}
