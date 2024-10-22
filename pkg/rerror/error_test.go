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
			return ctrl.Result{}, AsRetryableError(errors.New("dummy"))
		}
		_, err := EvaluateReconcileError(retryable())
		Expect(errors.Is(err, reconcile.TerminalError(nil))).To(BeFalse())
	})
	It("test as non retryable error", func() {
		nonRetryable := func() (ctrl.Result, ReconcileError) {
			return ctrl.Result{}, AsNonRetryableError(errors.New("dummy"))
		}
		_, err := EvaluateReconcileError(nonRetryable())
		Expect(errors.Is(err, reconcile.TerminalError(nil))).To(BeTrue())
	})
	It("test as retryable error wrapping non retryable", func() {
		nonRetryable := func() (ctrl.Result, ReconcileError) {
			return ctrl.Result{}, AsRetryableError(AsNonRetryableError(errors.New("dummy")))
		}
		_, err := EvaluateReconcileError(nonRetryable())
		Expect(errors.Is(err, reconcile.TerminalError(nil))).To(BeFalse())
	})
	It("test as non retryable error wrapping retryable", func() {
		nonRetryable := func() (ctrl.Result, ReconcileError) {
			return ctrl.Result{}, AsNonRetryableError(AsRetryableError(errors.New("dummy")))
		}
		_, err := EvaluateReconcileError(nonRetryable())
		Expect(errors.Is(err, reconcile.TerminalError(nil))).To(BeTrue())
	})
	It("tests retryable error is nil", func() {
		retryable := func() (ctrl.Result, ReconcileError) {
			return ctrl.Result{}, AsRetryableError(nil)
		}
		_, err := EvaluateReconcileError(retryable())
		Expect(err).To(BeNil())
	})
	It("tests non retryable error is nil", func() {
		nonRetryable := func() (ctrl.Result, ReconcileError) {
			return ctrl.Result{}, AsNonRetryableError(nil)
		}
		_, err := EvaluateReconcileError(nonRetryable())
		Expect(err).To(BeNil())
	})
})
