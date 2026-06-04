package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// composeFile is the minimal projection of docker-compose.yml needed
// for structural assertions. Unmarshaled via yaml.v3.
type composeFile struct {
	Services map[string]composeService `yaml:"services"`
	Networks map[string]any            `yaml:"networks"`
	Volumes  map[string]any            `yaml:"volumes"`
}

type composeService struct {
	Image         string            `yaml:"image"`
	ContainerName string            `yaml:"container_name"`
	Environment   map[string]string `yaml:"environment"`
	Ports         []string          `yaml:"ports"`
	Volumes       []string          `yaml:"volumes"`
	DependsOn     map[string]any    `yaml:"depends_on"`
	Healthcheck   map[string]any    `yaml:"healthcheck"`
}

// emit runs the generator with `f` and returns the produced YAML body.
func emit(t *testing.T, f dbClusterFlags) string {
	t.Helper()
	var buf bytes.Buffer
	if err := emitDBClusterCompose(&buf, f); err != nil {
		t.Fatalf("emit: %v", err)
	}
	return buf.String()
}

// parseCompose strips kit's header comments (yaml.v3 tolerates them
// already, but we re-test that the body parses cleanly) and unmarshals.
func parseCompose(t *testing.T, src string) composeFile {
	t.Helper()
	var out composeFile
	if err := yaml.Unmarshal([]byte(src), &out); err != nil {
		t.Fatalf("yaml unmarshal: %v\n---\n%s", err, src)
	}
	return out
}

func defaultFlags() dbClusterFlags {
	return dbClusterFlags{
		replicas:     1,
		image:        "bitnami/postgresql:16",
		database:     "appdb",
		username:     "app",
		password:     "changeme",
		replUser:     "repl",
		replPassword: "changeme",
		primaryPort:  5432,
		portBase:     5433,
		volumePrefix: "pgdata",
	}
}

func TestEmit_DefaultsProducePrimaryAndOneReplica(t *testing.T) {
	src := emit(t, defaultFlags())
	cf := parseCompose(t, src)
	if _, ok := cf.Services["primary"]; !ok {
		t.Error("missing primary service")
	}
	if _, ok := cf.Services["standby-1"]; !ok {
		t.Error("missing standby-1 service")
	}
	if got := len(cf.Services); got != 2 {
		t.Errorf("services count = %d, want 2", got)
	}
}

func TestEmit_ReplicaCount(t *testing.T) {
	f := defaultFlags()
	f.replicas = 3
	cf := parseCompose(t, emit(t, f))
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("standby-%d", i)
		if _, ok := cf.Services[name]; !ok {
			t.Errorf("missing %s service", name)
		}
	}
	if got := len(cf.Services); got != 4 {
		t.Errorf("services count = %d, want 4 (1 primary + 3 standby)", got)
	}
}

func TestEmit_PrimaryEnvVars(t *testing.T) {
	cf := parseCompose(t, emit(t, defaultFlags()))
	env := cf.Services["primary"].Environment
	cases := map[string]string{
		"POSTGRESQL_REPLICATION_MODE":     "master",
		"POSTGRESQL_REPLICATION_USER":     "repl",
		"POSTGRESQL_REPLICATION_PASSWORD": "changeme",
		"POSTGRESQL_USERNAME":             "app",
		"POSTGRESQL_PASSWORD":             "changeme",
		"POSTGRESQL_POSTGRES_PASSWORD":    "changeme",
		"POSTGRESQL_DATABASE":             "appdb",
	}
	for k, want := range cases {
		if got := env[k]; got != want {
			t.Errorf("primary.environment[%s] = %q, want %q", k, got, want)
		}
	}
	if _, leaked := env["POSTGRESQL_MASTER_HOST"]; leaked {
		t.Error("primary should NOT have POSTGRESQL_MASTER_HOST")
	}
}

func TestEmit_StandbyEnvVarsAndMasterHost(t *testing.T) {
	cf := parseCompose(t, emit(t, defaultFlags()))
	env := cf.Services["standby-1"].Environment
	if got := env["POSTGRESQL_REPLICATION_MODE"]; got != "slave" {
		t.Errorf("standby REPLICATION_MODE = %q, want slave", got)
	}
	if got := env["POSTGRESQL_MASTER_HOST"]; got != "primary" {
		t.Errorf("standby MASTER_HOST = %q, want primary", got)
	}
	if got := env["POSTGRESQL_MASTER_PORT_NUMBER"]; got != "5432" {
		t.Errorf("standby MASTER_PORT_NUMBER = %q, want 5432", got)
	}
}

func TestEmit_PortMappings(t *testing.T) {
	f := defaultFlags()
	f.replicas = 2
	cf := parseCompose(t, emit(t, f))
	if got := cf.Services["primary"].Ports[0]; got != "5432:5432" {
		t.Errorf("primary port = %q, want 5432:5432", got)
	}
	if got := cf.Services["standby-1"].Ports[0]; got != "5433:5432" {
		t.Errorf("standby-1 port = %q, want 5433:5432", got)
	}
	if got := cf.Services["standby-2"].Ports[0]; got != "5434:5432" {
		t.Errorf("standby-2 port = %q, want 5434:5432", got)
	}
}

func TestEmit_StandbyDependsOnPrimary(t *testing.T) {
	cf := parseCompose(t, emit(t, defaultFlags()))
	dep, ok := cf.Services["standby-1"].DependsOn["primary"].(map[string]any)
	if !ok {
		t.Fatalf("standby-1.depends_on.primary missing or wrong shape: %#v",
			cf.Services["standby-1"].DependsOn)
	}
	if got := dep["condition"]; got != "service_healthy" {
		t.Errorf("depends_on.primary.condition = %v, want service_healthy", got)
	}
}

func TestEmit_VolumesAndNetwork(t *testing.T) {
	f := defaultFlags()
	f.replicas = 2
	cf := parseCompose(t, emit(t, f))
	for _, name := range []string{"pgdata-primary", "pgdata-standby-1", "pgdata-standby-2"} {
		if _, ok := cf.Volumes[name]; !ok {
			t.Errorf("missing volume %q", name)
		}
	}
	defaultNet, _ := cf.Networks["default"].(map[string]any)
	if got := defaultNet["name"]; got != "pgdata-net" {
		t.Errorf("network name = %v, want pgdata-net", got)
	}
}

func TestEmit_HeaderRendersReadURLs(t *testing.T) {
	f := defaultFlags()
	f.replicas = 2
	src := emit(t, f)
	want := "DB_READ_URLS=postgres://app:changeme@localhost:5433/appdb,postgres://app:changeme@localhost:5434/appdb"
	if !strings.Contains(src, want) {
		t.Errorf("header missing expected READ_URLS line: %q\n---\n%s", want, headTail(src, 25))
	}
}

func TestEmit_CustomVolumePrefix(t *testing.T) {
	f := defaultFlags()
	f.volumePrefix = "mycluster"
	cf := parseCompose(t, emit(t, f))
	if _, ok := cf.Volumes["mycluster-primary"]; !ok {
		t.Error("custom volume-prefix not applied to primary volume")
	}
	if _, ok := cf.Volumes["mycluster-standby-1"]; !ok {
		t.Error("custom volume-prefix not applied to standby-1 volume")
	}
	defaultNet, _ := cf.Networks["default"].(map[string]any)
	if got := defaultNet["name"]; got != "mycluster-net" {
		t.Errorf("network name = %v, want mycluster-net", got)
	}
}

func TestEmit_HealthcheckPresent(t *testing.T) {
	cf := parseCompose(t, emit(t, defaultFlags()))
	for _, name := range []string{"primary", "standby-1"} {
		hc := cf.Services[name].Healthcheck
		if hc == nil {
			t.Errorf("%s missing healthcheck", name)
			continue
		}
		test, _ := hc["test"].([]any)
		if len(test) == 0 || test[0] != "CMD" {
			t.Errorf("%s healthcheck.test malformed: %v", name, hc["test"])
		}
	}
}

// headTail returns the first n lines + the last n lines of s with a
// `…` separator — used by error messages to keep failures readable.
func headTail(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= 2*n {
		return s
	}
	return strings.Join(lines[:n], "\n") + "\n…\n" + strings.Join(lines[len(lines)-n:], "\n")
}
