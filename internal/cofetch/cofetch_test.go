package cofetch

import (
	"testing"
)

func TestKubeconfigFiles_FromEnv(t *testing.T) {
	env := map[string]string{
		"KUBECONFIG": "/a/config:/b/config:/c/config",
	}
	got := KubeconfigFiles(func(k string) string { return env[k] })

	want := []string{"/a/config", "/b/config", "/c/config"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestKubeconfigFiles_SinglePathFromEnv(t *testing.T) {
	env := map[string]string{"KUBECONFIG": "/custom/config"}
	got := KubeconfigFiles(func(k string) string { return env[k] })

	if len(got) != 1 || got[0] != "/custom/config" {
		t.Errorf("got %v, want [/custom/config]", got)
	}
}

func TestKubeconfigFiles_DefaultWhenEnvEmpty(t *testing.T) {
	env := map[string]string{"HOME": "/home/user"}
	got := KubeconfigFiles(func(k string) string { return env[k] })

	if len(got) != 1 || got[0] != "/home/user/.kube/config" {
		t.Errorf("got %v, want [/home/user/.kube/config]", got)
	}
}

func TestKubeconfigFiles_DefaultWhenNoHome(t *testing.T) {
	got := KubeconfigFiles(func(k string) string { return "" })

	if len(got) != 1 || got[0] != "./.kube/config" {
		t.Errorf("got %v, want [./.kube/config]", got)
	}
}
