package ocm_test

import (
	"context"
	"encoding/base64"
	"encoding/pem"
	"github.com/Masterminds/semver/v3"
	. "github.com/mandelsoft/goutils/testutils"
	"github.com/mandelsoft/vfs/pkg/vfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	. "github.com/open-component-model/ocm-k8s-toolkit/internal/pkg/ocm"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"ocm.software/ocm/api/credentials/builtin/maven/identity"
	"ocm.software/ocm/api/datacontext"
	. "ocm.software/ocm/api/helper/builder"
	ocmctx "ocm.software/ocm/api/ocm"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	resourcetypes "ocm.software/ocm/api/ocm/extensions/artifacttypes"
	"ocm.software/ocm/api/ocm/extensions/attrs/signingattr"
	"ocm.software/ocm/api/ocm/extensions/repositories/ctf"
	"ocm.software/ocm/api/ocm/tools/signing"
	"ocm.software/ocm/api/tech/signing/handlers/rsa"
	"ocm.software/ocm/api/tech/signing/signutils"
	"ocm.software/ocm/api/utils/accessio"
	"ocm.software/ocm/api/utils/accessobj"
	"ocm.software/ocm/api/utils/mime"
	common "ocm.software/ocm/api/utils/misc"
)

const (
	CTFPATH       = "/ctf"
	COMPONENT     = "ocm.software/test"
	REFERENCE     = "referenced-test"
	REF_COMPONENT = "ocm.software/referenced-test"
	RESOURCE      = "testresource"
	VERSION1      = "1.0.0-rc.1"
	VERSION2      = "2.0.0"
	VERSION3      = "3.0.0"

	SIGNATURE1 = "signature1"
	SIGNATURE2 = "signature2"
	SIGNATURE3 = "signature3"

	CONFIG1 = "config1"
	CONFIG2 = "config2"
	CONFIG3 = "config3"

	SECRET1 = "secret1"
	SECRET2 = "secret2"
	SECRET3 = "secret3"

	SIGNSECRET1 = "signsecret1"
	SIGNSECRET2 = "signsecret2"
)

var _ = Describe("ocm utils", func() {
	var (
		ctx context.Context
		env *Builder
	)

	Context("configure context", func() {
		var (
			repo ocmctx.Repository
			cv   ocmctx.ComponentVersionAccess

			componentObj v1alpha1.Component

			configs []corev1.ConfigMap
			secrets []corev1.Secret
		)

		BeforeEach(func() {
			ctx = context.Background()
			_ = ctx
			env = NewBuilder()

			By("setup ocm")
			privkey1, pubkey1 := Must2(rsa.CreateKeyPair())
			privkey2, pubkey2 := Must2(rsa.CreateKeyPair())
			privkey3, pubkey3 := Must2(rsa.CreateKeyPair())

			env.OCMCommonTransport(CTFPATH, accessio.FormatDirectory, func() {
				env.Component(COMPONENT, func() {
					env.Version(VERSION1, func() {
					})
				})
			})

			repo = Must(ctf.Open(env, accessobj.ACC_WRITABLE, CTFPATH, vfs.FileMode(vfs.O_RDWR), env))
			cv = Must(repo.LookupComponentVersion(COMPONENT, VERSION1))

			_ = Must(signing.SignComponentVersion(cv, SIGNATURE1, signing.PrivateKey(SIGNATURE1, privkey1)))
			_ = Must(signing.SignComponentVersion(cv, SIGNATURE2, signing.PrivateKey(SIGNATURE2, privkey2)))
			_ = Must(signing.SignComponentVersion(cv, SIGNATURE3, signing.PrivateKey(SIGNATURE3, privkey3)))

			By("setup signsecrets")
			signsecret1 := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: SIGNSECRET1,
				},
				Data: map[string][]byte{
					SIGNATURE2: pem.EncodeToMemory(signutils.PemBlockForPublicKey(pubkey2)),
				},
			}
			secrets = append(secrets, signsecret1)

			signsecret2 := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: SIGNSECRET2,
				},
				Data: map[string][]byte{
					SIGNATURE3: pem.EncodeToMemory(signutils.PemBlockForPublicKey(pubkey3)),
				},
			}
			secrets = append(secrets, signsecret2)

			By("setup configs")
			config1 := corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: CONFIG1,
				},
				Data: map[string]string{
					v1alpha1.OCMConfigKey: `
type: generic.config.ocm.software/v1
sets:
  set1:
    description: set1
    configurations:
    - type: credentials.config.ocm.software
      consumers:
      - identity:
          type: MavenRepository
          hostname: example.com
          pathprefix: path/ocm
        credentials:
        - type: Credentials
          properties:
            username: testuser1
            password: testpassword1 
`,
				},
			}
			configs = append(configs, config1)

			config2 := corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: CONFIG2,
				},
				Data: map[string]string{
					v1alpha1.OCMConfigKey: `
type: generic.config.ocm.software/v1
sets:
  set2:
    description: set2
    configurations:
    - type: credentials.config.ocm.software
      consumers:
      - identity:
          type: MavenRepository
          hostname: example.com
          pathprefix: path/ocm
        credentials:
        - type: Credentials
          properties:
            username: testuser1
            password: testpassword1 
`,
				},
			}
			configs = append(configs, config2)

			config3 := corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: CONFIG3,
				},
				Data: map[string]string{
					v1alpha1.OCMConfigKey: `
type: generic.config.ocm.software/v1
sets:
  set3:
    description: set3
    configurations:
    - type: credentials.config.ocm.software
      consumers:
      - identity:
          type: MavenRepository
          hostname: example.com
          pathprefix: path/ocm
        credentials:
        - type: Credentials
          properties:
            username: testuser1
            password: testpassword1 
`,
				},
			}
			configs = append(configs, config3)

			By("setup secrets")
			secret1 := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: SECRET1,
				},
				Data: map[string][]byte{
					v1alpha1.OCMCredentialConfigKey: []byte(`
type: credentials.config.ocm.software
consumers:
- identity:
    type: MavenRepository
    hostname: example.com
    pathprefix: path1
  credentials:
  - type: Credentials
    properties:
      username: testuser1
      password: testpassword1
`),
				},
			}
			secrets = append(secrets, secret1)

			secret2 := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: SECRET2,
				},
				Data: map[string][]byte{
					v1alpha1.OCMCredentialConfigKey: []byte(`
type: credentials.config.ocm.software
consumers:
- identity:
    type: MavenRepository
    hostname: example.com
    pathprefix: path2
  credentials:
  - type: Credentials
    properties:
      username: testuser2
      password: testpassword2
`),
				},
			}
			secrets = append(secrets, secret2)

			secret3 := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: SECRET3,
				},
				Data: map[string][]byte{
					v1alpha1.OCMCredentialConfigKey: []byte(`
type: credentials.config.ocm.software
consumers:
- identity:
    type: MavenRepository
    hostname: example.com
    pathprefix: path3
  credentials:
  - type: Credentials
    properties:
      username: testuser3
      password: testpassword3
`),
				},
			}
			secrets = append(secrets, secret3)

			componentObj = v1alpha1.Component{
				Spec: v1alpha1.ComponentSpec{
					SecretRef: &corev1.LocalObjectReference{
						Name: "secret1",
					},
					SecretRefs: []corev1.LocalObjectReference{
						{Name: "secret2"},
						{Name: "secret3"},
					},
					ConfigRef: &corev1.LocalObjectReference{
						Name: CONFIG1,
					},
					ConfigRefs: []corev1.LocalObjectReference{
						{Name: CONFIG2},
						{Name: CONFIG3},
					},
					Verify: []v1alpha1.Verification{
						{
							Signature: SIGNATURE1,
							SecretRef: "",
							Value:     base64.StdEncoding.EncodeToString(pem.EncodeToMemory(signutils.PemBlockForPublicKey(pubkey1))),
						},
						{
							Signature: SIGNATURE2,
							SecretRef: SIGNSECRET1,
							Value:     "",
						},
						{
							Signature: SIGNATURE3,
							SecretRef: SIGNSECRET2,
							Value:     "",
						},
					},
				},
			}
		})

		AfterEach(func() {
			Close(cv)
			Close(repo)
			MustBeSuccessful(env.Cleanup())
		})

		It("configure context", func() {
			octx := ocmctx.New(datacontext.MODE_EXTENDED)
			MustBeSuccessful(ConfigureContext(octx, &componentObj, secrets, configs))

			creds1 := Must(octx.CredentialsContext().GetCredentialsForConsumer(Must(identity.GetConsumerId("https://example.com/path1", "")), identity.IdentityMatcher))
			creds2 := Must(octx.CredentialsContext().GetCredentialsForConsumer(Must(identity.GetConsumerId("https://example.com/path2", "")), identity.IdentityMatcher))
			creds3 := Must(octx.CredentialsContext().GetCredentialsForConsumer(Must(identity.GetConsumerId("https://example.com/path3", "")), identity.IdentityMatcher))
			Expect(Must(creds1.Credentials(octx.CredentialsContext())).Properties().Equals(common.Properties{
				"username": "testuser1",
				"password": "testpassword1",
			})).To(BeTrue())
			Expect(Must(creds2.Credentials(octx.CredentialsContext())).Properties().Equals(common.Properties{
				"username": "testuser2",
				"password": "testpassword2",
			})).To(BeTrue())
			Expect(Must(creds3.Credentials(octx.CredentialsContext())).Properties().Equals(common.Properties{
				"username": "testuser3",
				"password": "testpassword3",
			})).To(BeTrue())

			signreg := signing.Registry(signingattr.Get(octx))
			_ = Must(signing.VerifyComponentVersion(cv, SIGNATURE1, signing.NewOptions(signreg)))
			_ = Must(signing.VerifyComponentVersion(cv, SIGNATURE2, signing.NewOptions(signreg)))
			_ = Must(signing.VerifyComponentVersion(cv, SIGNATURE3, signing.NewOptions(signreg)))

			MustBeSuccessful(octx.ConfigContext().ApplyConfigSet("set1"))
			MustBeSuccessful(octx.ConfigContext().ApplyConfigSet("set2"))
			MustBeSuccessful(octx.ConfigContext().ApplyConfigSet("set3"))
		})
	})

	Context("get latest valid component version and regex filter", func() {
		var (
			repo ocmctx.Repository
			c    ocmctx.ComponentAccess
		)
		BeforeEach(func() {
			ctx = context.Background()
			_ = ctx
			env = NewBuilder()

			env.OCMCommonTransport(CTFPATH, accessio.FormatDirectory, func() {
				env.Component(COMPONENT, func() {
					env.Version(VERSION1, func() {
					})
				})
				env.Component(COMPONENT, func() {
					env.Version(VERSION2, func() {
					})
				})
				env.Component(COMPONENT, func() {
					env.Version(VERSION3, func() {
					})
				})
			})
			repo = Must(ctf.Open(env, accessobj.ACC_WRITABLE, CTFPATH, vfs.FileMode(vfs.O_RDWR), env))
			c = Must(repo.LookupComponent(COMPONENT))
		})

		AfterEach(func() {
			Close(c)
			Close(repo)
			MustBeSuccessful(env.Cleanup())
		})
		It("without filter", func() {
			ver := Must(GetLatestValidVersion(Must(c.ListVersions()), "<2.5.0"))
			Expect(ver.Equal(Must(semver.NewVersion(VERSION2))))
		})
		It("with filter", func() {
			ver := Must(GetLatestValidVersion(Must(c.ListVersions()), "<2.5.0", Must(RegexpFilter(".*-rc.*"))))
			Expect(ver.Equal(Must(semver.NewVersion(VERSION1))))
		})
	})

	Context("verify component version", func() {
		var (
			octx ocmctx.Context
			repo ocmctx.Repository
			cv   ocmctx.ComponentVersionAccess
		)

		BeforeEach(func() {
			ctx = context.Background()
			_ = ctx
			env = NewBuilder()

			By("setup ocm")
			privkey1, pubkey1 := Must2(rsa.CreateKeyPair())
			privkey2, pubkey2 := Must2(rsa.CreateKeyPair())
			privkey3, pubkey3 := Must2(rsa.CreateKeyPair())

			env.OCMCommonTransport(CTFPATH, accessio.FormatDirectory, func() {
				env.Component(COMPONENT, func() {
					env.Version(VERSION1, func() {
						env.Resource(RESOURCE, VERSION1, resourcetypes.PLAIN_TEXT, v1.LocalRelation, func() {
							env.BlobData(mime.MIME_TEXT, []byte("testdata"))
						})
						env.Reference(REFERENCE, REF_COMPONENT, VERSION2, func() {
						})
					})
				})
				env.Component(REF_COMPONENT, func() {
					env.Version(VERSION2, func() {
					})
				})
			})
			repo = Must(ctf.Open(env, accessobj.ACC_WRITABLE, CTFPATH, vfs.FileMode(vfs.O_RDWR), env))
			cv = Must(repo.LookupComponentVersion(COMPONENT, VERSION1))

			opts := signing.NewOptions(
				signing.Resolver(repo),
			)
			_ = Must(signing.SignComponentVersion(cv, SIGNATURE1, signing.PrivateKey(SIGNATURE1, privkey1), opts))
			_ = Must(signing.SignComponentVersion(cv, SIGNATURE2, signing.PrivateKey(SIGNATURE2, privkey2), opts))
			_ = Must(signing.SignComponentVersion(cv, SIGNATURE3, signing.PrivateKey(SIGNATURE3, privkey3), opts))

			octx = env.OCMContext()
			signreg := signingattr.Get(octx)
			signreg.RegisterPublicKey(SIGNATURE1, pubkey1)
			signreg.RegisterPublicKey(SIGNATURE2, pubkey2)
			signreg.RegisterPublicKey(SIGNATURE3, pubkey3)
		})

		AfterEach(func() {
			Close(cv)
			Close(repo)
			MustBeSuccessful(env.Cleanup())
		})

		It("without retrieving descriptors", func() {
			MustBeSuccessful(VerifyComponentVersion(cv, []string{SIGNATURE1, SIGNATURE2, SIGNATURE3}))
		})
		It("with retrieving descriptors", func() {
			descriptors := Must(VerifyComponentVersion(cv, []string{SIGNATURE1, SIGNATURE2, SIGNATURE3}))
			Expect(descriptors).To(HaveLen(2))
		})
		It("list component versions without verification", func() {
			descriptors := Must(ListComponentDescriptors(cv, repo))
			Expect(descriptors).To(HaveLen(2))
		})
	})

	Context("is downgradable", func() {
		var (
			ctx context.Context
			env *Builder

			repo ocmctx.Repository
			cv1  ocmctx.ComponentVersionAccess
			cv2  ocmctx.ComponentVersionAccess
			cv3  ocmctx.ComponentVersionAccess
		)
		BeforeEach(func() {
			ctx = context.Background()
			_ = ctx
			env = NewBuilder()

			v1 := "2.0.0"
			v2 := "1.1.0"
			v3 := "0.9.0"
			env.OCMCommonTransport(CTFPATH, accessio.FormatDirectory, func() {
				env.Component(COMPONENT, func() {
					env.Version(v1, func() {
						env.Label(v1alpha1.OCMLabelDowngradable, `> 1.0.0`)
					})
				})
				env.Component(COMPONENT, func() {
					env.Version(v2, func() {
					})
				})
				env.Component(COMPONENT, func() {
					env.Version(v3, func() {
					})
				})
			})

			repo = Must(ctf.Open(env, accessobj.ACC_WRITABLE, CTFPATH, vfs.FileMode(vfs.O_RDWR), env))
			cv1 = Must(repo.LookupComponentVersion(COMPONENT, v1))
			cv2 = Must(repo.LookupComponentVersion(COMPONENT, v2))
			cv3 = Must(repo.LookupComponentVersion(COMPONENT, v3))
		})

		AfterEach(func() {
			Close(cv1)
			Close(cv2)
			Close(cv3)
			Close(repo)
			MustBeSuccessful(env.Cleanup())
		})

		It("true", func() {
			Expect(Must(IsDowngradable(ctx, cv1, cv2))).To(BeTrue())
		})
		It("false", func() {
			Expect(Must(IsDowngradable(ctx, cv1, cv3))).To(BeFalse())
		})
	})
})
