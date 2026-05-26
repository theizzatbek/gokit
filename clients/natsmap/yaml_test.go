package natsmap

import (
	"os"
	"strings"
	"testing"
)

func TestSubstituteEnv_Happy(t *testing.T) {
	t.Setenv("NATSMAP_TEST_VAR", "value")
	out, err := substituteEnv([]byte("x=${NATSMAP_TEST_VAR}"), nil)
	if err != nil {
		t.Fatalf("substituteEnv: %v", err)
	}
	if string(out) != "x=value" {
		t.Fatalf("got %q", string(out))
	}
}

func TestSubstituteEnv_Unset(t *testing.T) {
	os.Unsetenv("NATSMAP_TEST_VAR_UNSET")
	_, err := substituteEnv([]byte("x=${NATSMAP_TEST_VAR_UNSET}"), nil)
	if err == nil || !strings.Contains(err.Error(), CodeEnvVarUnset) {
		t.Fatalf("want CodeEnvVarUnset, got %v", err)
	}
}

func TestSubstituteEnv_Malformed(t *testing.T) {
	_, err := substituteEnv([]byte("x=${not valid}"), nil)
	if err == nil || !strings.Contains(err.Error(), CodeEnvVarMalformed) {
		t.Fatalf("want CodeEnvVarMalformed, got %v", err)
	}
}
