package codeartifact_test

import (
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"github.com/truvity/artifacts/pkg/codeartifact"
)

const (
	domainType = "aws:codeartifact/domain:Domain"
	repoType   = "aws:codeartifact/repository:Repository"
)

type registration struct {
	typ, name, importID string
	protect             bool
	deps                []string
	inputs              resource.PropertyMap
}

type recorder struct {
	mu   sync.Mutex
	regs []registration
}

func (r *recorder) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	reg := registration{typ: args.TypeToken, name: args.Name, inputs: args.Inputs}

	if rpc := args.RegisterRPC; rpc != nil {
		reg.importID = rpc.GetImportId()
		reg.protect = rpc.GetProtect()
		reg.deps = rpc.GetDependencies()
	}

	r.mu.Lock()
	r.regs = append(r.regs, reg)
	r.mu.Unlock()

	out := args.Inputs.Copy()
	if args.TypeToken == domainType {
		out["owner"] = resource.NewStringProperty("111122223333")
	}

	return args.Name + "-id", out, nil
}

func (*recorder) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) { return args.Args, nil }

func base() codeartifact.Config {
	return codeartifact.Config{
		Domain: "example",
		Repositories: []codeartifact.Repository{
			{Name: "npm-store", ExternalConnection: "public:npmjs"},
			{Name: "npm-private", Upstreams: []string{"npm-store"}},
		},
	}
}

func run(t *testing.T, cfg codeartifact.Config, withProvider bool) ([]registration, error) {
	t.Helper()

	rec := &recorder{}

	err := pulumi.RunErr(func(c *pulumi.Context) error {
		if withProvider {
			p, err := aws.NewProvider(c, "aws-main", &aws.ProviderArgs{})
			if err != nil {
				return err
			}

			cfg.Provider = p
		}

		_, err := codeartifact.Deploy(c, nil, cfg)

		return err
	}, pulumi.WithMocks("test", "test", rec))

	var out []registration

	for _, r := range rec.regs {
		if !strings.HasPrefix(r.typ, "pulumi:providers:") {
			out = append(out, r)
		}
	}

	return out, err
}

func find(t *testing.T, regs []registration, typ, name string) registration {
	t.Helper()

	for _, r := range regs {
		if r.typ == typ && r.name == name {
			return r
		}
	}

	t.Fatalf("no %s %s registered", typ, name)

	return registration{}
}

func str(r registration, key string) string {
	if v, ok := r.inputs[resource.PropertyKey(key)]; ok && v.IsString() {
		return v.StringValue()
	}

	return ""
}

func TestNamesAreAPI(t *testing.T) {
	regs, err := run(t, base(), true)
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, r := range regs {
		got = append(got, r.typ+" "+r.name)
	}

	sort.Strings(got)

	want := []string{
		domainType + " codeartifact-example",
		repoType + " codeartifact-example-npm-private",
		repoType + " codeartifact-example-npm-store",
	}

	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("registered:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestInputs(t *testing.T) {
	regs, err := run(t, base(), true)
	if err != nil {
		t.Fatal(err)
	}

	d := find(t, regs, domainType, "codeartifact-example")
	if str(d, "domain") != "example" {
		t.Errorf("domain %v", d.inputs)
	}

	for _, key := range []string{"encryptionKey", "tags"} {
		if _, ok := d.inputs[resource.PropertyKey(key)]; ok {
			t.Errorf("domain sets %s though the config states none", key)
		}
	}

	store := find(t, regs, repoType, "codeartifact-example-npm-store")
	if str(store, "repository") != "npm-store" || str(store, "domain") != "example" {
		t.Errorf("store %v", store.inputs)
	}

	if got := store.inputs["externalConnections"].ObjectValue()["externalConnectionName"].StringValue(); got != "public:npmjs" {
		t.Errorf("store external connection %q", got)
	}

	for _, key := range []string{"upstreams", "description", "domainOwner"} {
		if _, ok := store.inputs[resource.PropertyKey(key)]; ok {
			t.Errorf("store sets %s", key)
		}
	}

	private := find(t, regs, repoType, "codeartifact-example-npm-private")

	ups := private.inputs["upstreams"].ArrayValue()
	if len(ups) != 1 || ups[0].ObjectValue()["repositoryName"].StringValue() != "npm-store" {
		t.Errorf("private upstreams %v", ups)
	}

	if _, ok := private.inputs["externalConnections"]; ok {
		t.Error("private has an external connection")
	}

	var dependsOnStore bool
	for _, dep := range private.deps {
		dependsOnStore = dependsOnStore || strings.HasSuffix(dep, "::codeartifact-example-npm-store")
	}

	if !dependsOnStore {
		t.Errorf("npm-private does not depend on its upstream: %v", private.deps)
	}
}

func TestExplicitKeyTagsAndDescription(t *testing.T) {
	cfg := base()
	cfg.EncryptionKeyARN = "arn:aws:kms:eu-west-1:111122223333:key/example"
	cfg.Tags = map[string]string{"team": "x"}
	cfg.Repositories[1].Description = "private packages"

	regs, err := run(t, cfg, true)
	if err != nil {
		t.Fatal(err)
	}

	d := find(t, regs, domainType, "codeartifact-example")
	if str(d, "encryptionKey") != cfg.EncryptionKeyARN || d.inputs["tags"].ObjectValue()["team"].StringValue() != "x" {
		t.Errorf("domain %v", d.inputs)
	}

	if got := str(find(t, regs, repoType, "codeartifact-example-npm-private"), "description"); got != "private packages" {
		t.Errorf("description %q", got)
	}
}

func TestAdoptImportsByARNAndCreateImportsNothing(t *testing.T) {
	regs, err := run(t, base(), true)
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range regs {
		if r.importID != "" {
			t.Errorf("%s %s imports %q without Adopt", r.typ, r.name, r.importID)
		}
	}

	cfg := base()
	cfg.Adopt = &codeartifact.Adoption{AccountID: "111122223333", Region: "eu-west-1"}

	regs, err = run(t, cfg, true)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"codeartifact-example":             "arn:aws:codeartifact:eu-west-1:111122223333:domain/example",
		"codeartifact-example-npm-store":   "arn:aws:codeartifact:eu-west-1:111122223333:repository/example/npm-store",
		"codeartifact-example-npm-private": "arn:aws:codeartifact:eu-west-1:111122223333:repository/example/npm-private",
	}

	for _, r := range regs {
		if r.importID != want[r.name] {
			t.Errorf("%s: import %q, want %q", r.name, r.importID, want[r.name])
		}
	}
}

func TestProtectedByDefault(t *testing.T) {
	for name, tc := range map[string]struct {
		protect *bool
		want    bool
	}{"nil": {nil, true}, "true": {ptr(true), true}, "false": {ptr(false), false}} {
		t.Run(name, func(t *testing.T) {
			cfg := base()
			cfg.Protect = tc.protect

			regs, err := run(t, cfg, true)
			if err != nil {
				t.Fatal(err)
			}

			for _, r := range regs {
				if r.protect != tc.want {
					t.Errorf("%s: protect %v, want %v", r.name, r.protect, tc.want)
				}
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }

func TestRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*codeartifact.Config)
		want   string
	}{
		"domain":          {func(c *codeartifact.Config) { c.Domain = "Bad_Domain" }, "is not a CodeArtifact domain name"},
		"repository name": {func(c *codeartifact.Config) { c.Repositories[0].Name = "-x" }, "Repositories[0].Name"},
		"duplicate":       {func(c *codeartifact.Config) { c.Repositories[1].Name = "npm-store" }, "duplicate name"},
		"upstream order":  {func(c *codeartifact.Config) { c.Repositories[0].Upstreams = []string{"npm-private"} }, "not declared before it"},
		"upstream twice":  {func(c *codeartifact.Config) { c.Repositories[1].Upstreams = []string{"npm-store", "npm-store"} }, "listed twice"},
		"connection":      {func(c *codeartifact.Config) { c.Repositories[0].ExternalConnection = "npmjs" }, "public:<registry>"},
		"adopt account":   {func(c *codeartifact.Config) { c.Adopt = &codeartifact.Adoption{AccountID: "x", Region: "r"} }, "Adopt.AccountID"},
		"adopt region":    {func(c *codeartifact.Config) { c.Adopt = &codeartifact.Adoption{AccountID: "111122223333"} }, "Adopt.Region"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := base()
			tc.mutate(&cfg)

			regs, err := run(t, cfg, true)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}

			if len(regs) != 0 {
				t.Errorf("registered %d resources before refusing", len(regs))
			}
		})
	}
}

func TestMissingProviderIsRefused(t *testing.T) {
	if _, err := run(t, base(), false); err == nil || !strings.Contains(err.Error(), "Provider is nil") {
		t.Fatalf("err = %v", err)
	}
}
