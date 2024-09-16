package rerror

import (
	"errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("test reconcile error", func() {

	It("test as retryable error", func() {
		retryable := func() (ctrl.Result, ReconcileError) {
			return ctrl.Result{}, AsRetryableError(nil)
		}
		_, err := EvaluateReconcileError(retryable())
		Expect(errors.Is(err, reconcile.TerminalError(nil))).To(BeFalse())
	})
	It("test as non retryable error", func() {
		nonretryable := func() (ctrl.Result, ReconcileError) {
			return ctrl.Result{}, AsNonRetryableError(nil)
		}
		_, err := EvaluateReconcileError(nonretryable())
		Expect(errors.Is(err, reconcile.TerminalError(nil))).To(BeTrue())
	})
	It("test as retryable error wrapping non retryable", func() {
		nonretryable := func() (ctrl.Result, ReconcileError) {
			return ctrl.Result{}, AsRetryableError(AsNonRetryableError(nil))
		}
		_, err := EvaluateReconcileError(nonretryable())
		Expect(errors.Is(err, reconcile.TerminalError(nil))).To(BeFalse())
	})
	It("test as non retryable error wrapping retryable", func() {
		nonretryable := func() (ctrl.Result, ReconcileError) {
			return ctrl.Result{}, AsNonRetryableError(AsRetryableError(nil))
		}
		_, err := EvaluateReconcileError(nonretryable())
		Expect(errors.Is(err, reconcile.TerminalError(nil))).To(BeTrue())
	})
})
