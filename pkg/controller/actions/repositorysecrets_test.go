/*
Copyright 2021 The Crossplane Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
// http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package repositorysecret

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/pkg/errors"

	"github.com/crossplane-contrib/provider-github/apis/actions/v1alpha1"
	gc "github.com/crossplane-contrib/provider-github/pkg/clients"
	RepositorySecret "github.com/crossplane-contrib/provider-github/pkg/clients/actions"
	"github.com/crossplane-contrib/provider-github/pkg/clients/actions/fake"
	v1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-github/v48/github"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	superSecret      = []byte("test")
	unexpectedObject resource.Managed
	errBoom          = errors.New("boom")
	fakeRepo         = "repo"
	fakeOwner        = "crossplane"
	fakeHashCorrect  = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	fakeHashFalse    = "fakeHash"
	fakeUpdateTime   = time.Time{}
)

type args struct {
	kube   client.Client
	mg     resource.Managed
	github RepositorySecret.Service
}

func mockMG(hash string) *v1alpha1.RepositorySecret {
	mg := v1alpha1.RepositorySecret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testSecret",
			Annotations: map[string]string{
				"crossplane.io/external-name": "TestSecret",
			},
		},
		Status: v1alpha1.RepositorySecretStatus{
			AtProvider: v1alpha1.RepositorySecretObservation{
				EncryptValue: hash,
				LastUpdate:   fakeUpdateTime.String(),
			},
		},
		Spec: v1alpha1.RepositorySecretSpec{
			ForProvider: v1alpha1.RepositorySecretParameters{
				Owner:      fakeOwner,
				Repository: fakeRepo,
				Value: v1.SecretKeySelector{
					SecretReference: v1.SecretReference{
						Name:      "test-repo-secret-secret-gh",
						Namespace: "crossplane-system",
					},
					Key: "test",
				},
			},
		},
	}

	return &mg
}

func TestObserve(t *testing.T) {
	type want struct {
		mg  managed.ExternalObservation
		err error
	}

	cases := map[string]struct {
		reason string
		args   args
		want   want
	}{
		"ResourceIsNotRepositorySecret": {
			reason: "Must return an error resource is not repository secret",
			args: args{
				mg: unexpectedObject,
			},
			want: want{
				mg:  managed.ExternalObservation{},
				err: errors.New(errUnexpectedObject),
			},
		},
		"GetRepositorySecretFailed": {
			reason: "Get repository secret fails",
			args: args{
				kube: test.NewMockClient(),
				mg:   mockMG(fakeHashCorrect),
				github: &fake.MockServiceRepositorySecret{
					MockGetRepoSecret: func(ctx context.Context, owner, repo, name string) (*github.Secret, *github.Response, error) {
						return &github.Secret{}, &github.Response{Response: &http.Response{StatusCode: 401}}, errBoom
					},
				},
			},
			want: want{
				mg:  managed.ExternalObservation{},
				err: errors.Wrap(errBoom, "cannot get repository secret from GitHub"),
			},
		},
		"RepositorySecretToCreation": {
			reason: "Repository secret needs to be created",
			args: args{
				kube: test.NewMockClient(),
				mg:   &v1alpha1.RepositorySecret{},
				github: &fake.MockServiceRepositorySecret{
					MockGetRepoSecret: func(ctx context.Context, owner, repo, name string) (*github.Secret, *github.Response, error) {
						return &github.Secret{}, &github.Response{Response: &http.Response{StatusCode: 404}}, errBoom
					},
				},
			},
			want: want{
				mg:  managed.ExternalObservation{},
				err: nil,
			},
		},
		"RepositorySecretUpdateFailed": {
			reason: "Repository secret up to date fails",
			args: args{
				kube: &test.MockClient{
					MockGet: test.NewMockGetFn(nil, func(obj client.Object) error {
						s, _ := obj.(*corev1.Secret)
						s.Data = map[string][]byte{
							"test": superSecret,
						}

						return errBoom
					}),
				},
				mg: mockMG(fakeHashCorrect),
				github: &fake.MockServiceRepositorySecret{
					MockGetRepoSecret: func(ctx context.Context, owner, repo, name string) (*github.Secret, *github.Response, error) {
						return &github.Secret{}, &github.Response{Response: &http.Response{StatusCode: 200}}, nil
					},
				},
			},
			want: want{
				mg:  managed.ExternalObservation{},
				err: errors.Wrap(errBoom, "Error to verify if is up to date: cannot get credentials secret"),
			},
		},
		"RepositorySecretToUpdate": {
			reason: "Repository secret needs to be updated",
			args: args{
				kube: test.NewMockClient(),
				github: &fake.MockServiceRepositorySecret{
					MockGetRepoSecret: func(ctx context.Context, owner, repo, name string) (*github.Secret, *github.Response, error) {
						return &github.Secret{Name: "TESTSECRET", CreatedAt: github.Timestamp{Time: fakeUpdateTime}, UpdatedAt: github.Timestamp{Time: fakeUpdateTime}}, &github.Response{Response: &http.Response{StatusCode: 200}}, nil
					},
				},
				mg: mockMG(fakeHashFalse),
			},
			want: want{
				mg: managed.ExternalObservation{
					ResourceExists:   true,
					ResourceUpToDate: false,
				},
				err: nil,
			},
		},
		"RepositorySecretUpToDate": {
			reason: "Repository Secret is up to date",
			args: args{
				kube: &test.MockClient{
					MockGet: test.NewMockGetFn(nil, func(obj client.Object) error {
						s, _ := obj.(*corev1.Secret)
						s.Data = map[string][]byte{
							"test": superSecret,
						}

						return nil
					}),
				},
				github: &fake.MockServiceRepositorySecret{
					MockGetRepoSecret: func(ctx context.Context, owner, repo, name string) (*github.Secret, *github.Response, error) {
						return &github.Secret{Name: "TESTSECRET", CreatedAt: github.Timestamp{Time: fakeUpdateTime}, UpdatedAt: github.Timestamp{Time: fakeUpdateTime}}, &github.Response{Response: &http.Response{StatusCode: 200}}, nil
					},
				},
				mg: mockMG(fakeHashCorrect),
			},
			want: want{
				mg: managed.ExternalObservation{
					ResourceExists:   true,
					ResourceUpToDate: true,
				},
				err: nil,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := external{
				client: tc.args.kube,
				gh:     tc.args.github,
			}
			got, err := e.Observe(context.Background(), tc.args.mg)
			if diff := cmp.Diff(tc.want.mg, got); diff != "" {
				t.Errorf("Observe(...): -want, +got:\n%s", diff)
			}
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("Observe(...): -want error, +got error:\n%s", diff)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	type want struct {
		mg  managed.ExternalCreation
		err error
	}

	cases := map[string]struct {
		reason string
		args   args
		want   want
	}{
		"ResourceIsNotRepositorySecret": {
			reason: "Must return an error resource is not a repository secret",
			args: args{
				mg: unexpectedObject,
			},
			want: want{
				mg:  managed.ExternalCreation{},
				err: errors.New(errUnexpectedObject),
			},
		},
		"CreationFailed": {
			reason: "Must return an error if the repository secret fails",
			args: args{
				kube: test.NewMockClient(),
				github: &fake.MockServiceRepositorySecret{
					MockCreateOrUpdateRepoSecret: func(ctx context.Context, owner, repo string, eSecret *github.EncryptedSecret) (*github.Response, error) {
						return &github.Response{}, errBoom
					},
					MockGetRepoPublicKey: func(ctx context.Context, owner, repo string) (*github.PublicKey, *github.Response, error) {
						return &github.PublicKey{KeyID: gc.StringPtr("172354871263548712365487"), Key: gc.StringPtr("ZjRrM2szeQ==")}, &github.Response{}, nil
					},
				},
				mg: mockMG(fakeHashCorrect),
			},
			want: want{
				mg:  managed.ExternalCreation{},
				err: errors.Wrap(errBoom, errCreateRepositorySecrets),
			},
		},
		"Success": {
			reason: "Must not return an error if everything goes well",
			args: args{
				kube: &test.MockClient{
					MockGet: test.NewMockGetFn(nil, func(obj client.Object) error {
						s, _ := obj.(*corev1.Secret)
						s.Data = map[string][]byte{
							"test": superSecret,
						}

						return nil
					}),
				},
				github: &fake.MockServiceRepositorySecret{
					MockCreateOrUpdateRepoSecret: func(ctx context.Context, owner, repo string, eSecret *github.EncryptedSecret) (*github.Response, error) {
						return &github.Response{}, nil
					},
					MockGetRepoSecret: func(ctx context.Context, owner, repo, name string) (*github.Secret, *github.Response, error) {
						return &github.Secret{Name: "TESTSECRET", CreatedAt: github.Timestamp{Time: fakeUpdateTime}, UpdatedAt: github.Timestamp{Time: fakeUpdateTime}}, &github.Response{}, nil
					},
					MockGetRepoPublicKey: func(ctx context.Context, owner, repo string) (*github.PublicKey, *github.Response, error) {
						return &github.PublicKey{KeyID: gc.StringPtr("172354871263548712365487"), Key: gc.StringPtr("ZjRrM2szeQ==")}, &github.Response{}, nil
					},
				},
				mg: mockMG(fakeHashCorrect),
			},
			want: want{
				mg:  managed.ExternalCreation{},
				err: nil,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := external{
				gh:     tc.args.github,
				client: tc.args.kube,
			}
			got, err := e.Create(context.Background(), tc.args.mg)
			if diff := cmp.Diff(tc.want.mg, got); diff != "" {
				t.Errorf("Create(...): -want, +got:\n%s", diff)
			}
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("Create(...): -want error, +got error:\n%s", diff)
			}
		})
	}
}

func TestUpdate(t *testing.T) {
	type want struct {
		mg  managed.ExternalUpdate
		err error
	}

	cases := map[string]struct {
		reason string
		args   args
		want   want
	}{
		"ResourceIsNotRepositorySecret": {
			reason: "Must return an error resource is not a repository secret",
			args: args{
				mg: unexpectedObject,
			},
			want: want{
				mg:  managed.ExternalUpdate{},
				err: errors.New(errUnexpectedObject),
			},
		},
		"UpdateFailed": {
			reason: "Must return an error if the repository secret fails",
			args: args{
				kube: test.NewMockClient(),
				github: &fake.MockServiceRepositorySecret{
					MockCreateOrUpdateRepoSecret: func(ctx context.Context, owner, repo string, eSecret *github.EncryptedSecret) (*github.Response, error) {
						return &github.Response{}, errBoom
					},
					MockGetRepoPublicKey: func(ctx context.Context, owner, repo string) (*github.PublicKey, *github.Response, error) {
						return &github.PublicKey{KeyID: gc.StringPtr("172354871263548712365487"), Key: gc.StringPtr("ZjRrM2szeQ==")}, &github.Response{}, nil
					},
				},
				mg: mockMG(fakeHashCorrect),
			},
			want: want{
				mg:  managed.ExternalUpdate{},
				err: errors.Wrap(errBoom, errUpdateRepositorySecrets),
			},
		},
		"Success": {
			reason: "Must not return an error if everything goes well",
			args: args{
				kube: &test.MockClient{
					MockGet: test.NewMockGetFn(nil, func(obj client.Object) error {
						s, _ := obj.(*corev1.Secret)
						s.Data = map[string][]byte{
							"test": superSecret,
						}

						return nil
					}),
				},
				github: &fake.MockServiceRepositorySecret{
					MockCreateOrUpdateRepoSecret: func(ctx context.Context, owner, repo string, eSecret *github.EncryptedSecret) (*github.Response, error) {
						return &github.Response{}, nil
					},
					MockGetRepoSecret: func(ctx context.Context, owner, repo, name string) (*github.Secret, *github.Response, error) {
						return &github.Secret{Name: "TESTSECRET", CreatedAt: github.Timestamp{Time: fakeUpdateTime}, UpdatedAt: github.Timestamp{Time: fakeUpdateTime}}, &github.Response{}, nil
					},
					MockGetRepoPublicKey: func(ctx context.Context, owner, repo string) (*github.PublicKey, *github.Response, error) {
						return &github.PublicKey{KeyID: gc.StringPtr("172354871263548712365487"), Key: gc.StringPtr("ZjRrM2szeQ==")}, &github.Response{}, nil
					},
				},
				mg: mockMG(fakeHashCorrect),
			},
			want: want{
				mg:  managed.ExternalUpdate{},
				err: nil,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := external{
				client: tc.args.kube,
				gh:     tc.args.github,
			}
			got, err := e.Update(context.Background(), tc.args.mg)
			if diff := cmp.Diff(tc.want.mg, got); diff != "" {
				t.Errorf("Update(...): -want, +got:\n%s", diff)
			}
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("Update(...): -want error, +got error:\n%s", diff)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	type want struct {
		err error
	}

	cases := map[string]struct {
		reason string
		args   args
		want   want
	}{
		"ResourceIsNotRepositorySecret": {
			reason: "Must return an error resource is not a repository secret",
			args: args{
				mg: unexpectedObject,
			},
			want: want{
				err: errors.New(errUnexpectedObject),
			},
		},
		"DeleteFailed": {
			reason: "Must return an error if delete the repository secret fails",
			args: args{
				github: &fake.MockServiceRepositorySecret{
					MockDeleteRepoSecret: func(ctx context.Context, owner, repo, name string) (*github.Response, error) {
						return &github.Response{}, errBoom
					},
				},
				mg: mockMG(fakeHashCorrect),
			},
			want: want{
				err: errors.Wrap(errBoom, errDeleteRepositorySecrets),
			},
		},
		"Success": {
			reason: "Delete the repository secret success case",
			args: args{
				github: &fake.MockServiceRepositorySecret{
					MockDeleteRepoSecret: func(ctx context.Context, owner, repo, name string) (*github.Response, error) {
						return &github.Response{}, nil
					},
				},
				mg: mockMG(fakeHashCorrect),
			},
			want: want{
				err: nil,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := external{
				gh: tc.args.github,
			}

			err := e.Delete(context.Background(), tc.args.mg)

			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("Update(...): -want error, +got error:\n%s", diff)
			}
		})
	}
}
