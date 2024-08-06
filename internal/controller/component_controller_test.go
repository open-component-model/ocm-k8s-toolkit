/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"reflect"
	"testing"

	"github.com/open-component-model/ocm-k8s-toolkit/internal/pkg/ocm"
	"github.com/openfluxcd/controller-manager/storage"
	"k8s.io/apimachinery/pkg/runtime"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestComponentReconciler_Reconcile(t *testing.T) {
	type fields struct {
		Client    client.Client
		Scheme    *runtime.Scheme
		Storage   *storage.Storage
		OCMClient ocm.Contract
	}
	type args struct {
		ctx context.Context
		req controllerruntime.Request
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    controllerruntime.Result
		wantErr bool
	}{
		{
			name: "normal reconciliation",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &ComponentReconciler{
				Client:    tt.fields.Client,
				Scheme:    tt.fields.Scheme,
				Storage:   tt.fields.Storage,
				OCMClient: tt.fields.OCMClient,
			}
			got, err := r.Reconcile(tt.args.ctx, tt.args.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("Reconcile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Reconcile() got = %v, want %v", got, tt.want)
			}
		})
	}
}
