package ocm_test

import (
	"context"
	"encoding/pem"

	"github.com/Masterminds/semver/v3"
	. "github.com/mandelsoft/goutils/testutils"
	"github.com/mandelsoft/vfs/pkg/vfs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"ocm.software/ocm/api/datacontext"
	. "ocm.software/ocm/api/helper/builder"
	ocmctx "ocm.software/ocm/api/ocm"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	resourcetypes "ocm.software/ocm/api/ocm/extensions/artifacttypes"
	"ocm.software/ocm/api/ocm/extensions/attrs/signingattr"
	"ocm.software/ocm/api/ocm/extensions/repositories/ctf"
	"ocm.software/ocm/api/ocm/tools/signing"
	"ocm.software/ocm/api/tech/maven/identity"
	"ocm.software/ocm/api/tech/signing/handlers/rsa"
	"ocm.software/ocm/api/tech/signing/signutils"
	"ocm.software/ocm/api/utils/accessio"
	"ocm.software/ocm/api/utils/accessobj"
	"ocm.software/ocm/api/utils/mime"
	common "ocm.software/ocm/api/utils/misc"
	
	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	. "github.com/open-component-model/ocm-k8s-toolkit/internal/pkg/ocm"
)

const (
	CTFPath      = "/ctf"
	Component    = "ocm.software/test"
	Reference    = "referenced-test"
	RefComponent = "ocm.software/referenced-test"
	Resource     = "testresource"
	Version1     = "1.0.0-rc.1"
	Version2     = "2.0.0"
	Version3     = "3.0.0"

	Signature1 = "signature1"
	Signature2 = "signature2"
	Signature3 = "signature3"

	Config1 = "config1"
	Config2 = "config2"
	Config3 = "config3"

	Secret1 = "secret1"
	Secret2 = "secret2"
	Secret3 = "secret3"

	SignSecret1 = "signsecret1"
	SignSecret2 = "signsecret2"
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

			configs       []*corev1.ConfigMap
			secrets       []*corev1.Secret
			verifications []Verification
		)

		BeforeEach(func() {
			ctx = context.Background()
			_ = ctx
			env = NewBuilder()

			By("setup ocm")
			privkey1, pubkey1 := Must2(rsa.CreateKeyPair())
			privkey2, pubkey2 := Must2(rsa.CreateKeyPair())
			privkey3, pubkey3 := Must2(rsa.CreateKeyPair())

			env.OCMCommonTransport(CTFPath, accessio.FormatDirectory, func() {
				env.Component(Component, func() {
					env.Version(Version1, func() {
					})
				})
			})

			repo = Must(ctf.Open(env, accessobj.ACC_WRITABLE, CTFPath, vfs.FileMode(vfs.O_RDWR), env))
			cv = Must(repo.LookupComponentVersion(Component, Version1))

			_ = Must(signing.SignComponentVersion(cv, Signature1, signing.PrivateKey(Signature1, privkey1)))
			_ = Must(signing.SignComponentVersion(cv, Signature2, signing.PrivateKey(Signature2, privkey2)))
			_ = Must(signing.SignComponentVersion(cv, Signature3, signing.PrivateKey(Signature3, privkey3)))

			By("setup signsecrets")
			verifications = append(verifications, []Verification{
				{Signature: Signature1, PublicKey: pem.EncodeToMemory(signutils.PemBlockForPublicKey(pubkey1))},
				{Signature: Signature2, PublicKey: pem.EncodeToMemory(signutils.PemBlockForPublicKey(pubkey2))},
				{Signature: Signature3, PublicKey: pem.EncodeToMemory(signutils.PemBlockForPublicKey(pubkey3))},
			}...)

			By("setup configs")
			config1 := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: Config1,
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

			config2 := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: Config2,
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

			config3 := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: Config3,
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
			secret1 := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: Secret1,
				},
				Data: map[string][]byte{
					v1alpha1.OCMConfigKey: []byte(`
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

			secret2 := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: Secret2,
				},
				Data: map[string][]byte{
					v1alpha1.OCMConfigKey: []byte(`
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
					Name: Secret3,
				},
				Data: map[string][]byte{
					v1alpha1.OCMConfigKey: []byte(`
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
			secrets = append(secrets, &secret3)
		})

		AfterEach(func() {
			Close(cv)
			Close(repo)
			MustBeSuccessful(env.Cleanup())
		})

		It("configure context", func() {
			octx := ocmctx.New(datacontext.MODE_EXTENDED)
			MustBeSuccessful(ConfigureContext(ctx, octx, verifications, secrets, configs))

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
			_ = Must(signing.VerifyComponentVersion(cv, Signature1, signing.NewOptions(signreg)))
			_ = Must(signing.VerifyComponentVersion(cv, Signature2, signing.NewOptions(signreg)))
			_ = Must(signing.VerifyComponentVersion(cv, Signature3, signing.NewOptions(signreg)))

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

			env.OCMCommonTransport(CTFPath, accessio.FormatDirectory, func() {
				env.Component(Component, func() {
					env.Version(Version1, func() {
					})
				})
				env.Component(Component, func() {
					env.Version(Version2, func() {
					})
				})
				env.Component(Component, func() {
					env.Version(Version3, func() {
					})
				})
			})
			repo = Must(ctf.Open(env, accessobj.ACC_WRITABLE, CTFPath, vfs.FileMode(vfs.O_RDWR), env))
			c = Must(repo.LookupComponent(Component))
		})

		AfterEach(func() {
			Close(c)
			Close(repo)
			MustBeSuccessful(env.Cleanup())
		})
		It("without filter", func() {
			ver := Must(GetLatestValidVersion(ctx, Must(c.ListVersions()), "<2.5.0"))
			Expect(ver.Equal(Must(semver.NewVersion(Version2))))
		})
		It("with filter", func() {
			ver := Must(GetLatestValidVersion(ctx, Must(c.ListVersions()), "<2.5.0", Must(RegexpFilter(".*-rc.*"))))
			Expect(ver.Equal(Must(semver.NewVersion(Version1))))
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

			env.OCMCommonTransport(CTFPath, accessio.FormatDirectory, func() {
				env.Component(Component, func() {
					env.Version(Version1, func() {
						env.Resource(Resource, Version1, resourcetypes.PLAIN_TEXT, v1.LocalRelation, func() {
							env.BlobData(mime.MIME_TEXT, []byte("testdata"))
						})
						env.Reference(Reference, RefComponent, Version2, func() {
						})
					})
				})
				env.Component(RefComponent, func() {
					env.Version(Version2, func() {
					})
				})
			})
			repo = Must(ctf.Open(env, accessobj.ACC_WRITABLE, CTFPath, vfs.FileMode(vfs.O_RDWR), env))
			cv = Must(repo.LookupComponentVersion(Component, Version1))

			opts := signing.NewOptions(
				signing.Resolver(repo),
			)
			_ = Must(signing.SignComponentVersion(cv, Signature1, signing.PrivateKey(Signature1, privkey1), opts))
			_ = Must(signing.SignComponentVersion(cv, Signature2, signing.PrivateKey(Signature2, privkey2), opts))
			_ = Must(signing.SignComponentVersion(cv, Signature3, signing.PrivateKey(Signature3, privkey3), opts))

			octx = env.OCMContext()
			signreg := signingattr.Get(octx)
			signreg.RegisterPublicKey(Signature1, pubkey1)
			signreg.RegisterPublicKey(Signature2, pubkey2)
			signreg.RegisterPublicKey(Signature3, pubkey3)
		})

		AfterEach(func() {
			Close(cv)
			Close(repo)
			MustBeSuccessful(env.Cleanup())
		})

		It("without retrieving descriptors", func() {
			MustBeSuccessful(VerifyComponentVersion(ctx, cv, []string{Signature1, Signature2, Signature3}))
		})
		It("with retrieving descriptors", func() {
			descriptors := Must(VerifyComponentVersion(ctx, cv, []string{Signature1, Signature2, Signature3}))
			Expect(descriptors.List).To(HaveLen(2))
		})
		It("list component versions without verification", func() {
			descriptors := Must(ListComponentDescriptors(ctx, cv, repo))
			Expect(descriptors.List).To(HaveLen(2))
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
			env.OCMCommonTransport(CTFPath, accessio.FormatDirectory, func() {
				env.Component(Component, func() {
					env.Version(v1, func() {
						env.Label(v1alpha1.OCMLabelDowngradable, `> 1.0.0`)
					})
				})
				env.Component(Component, func() {
					env.Version(v2, func() {
					})
				})
				env.Component(Component, func() {
					env.Version(v3, func() {
					})
				})
			})

			repo = Must(ctf.Open(env, accessobj.ACC_WRITABLE, CTFPath, vfs.FileMode(vfs.O_RDWR), env))
			cv1 = Must(repo.LookupComponentVersion(Component, v1))
			cv2 = Must(repo.LookupComponentVersion(Component, v2))
			cv3 = Must(repo.LookupComponentVersion(Component, v3))
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
