package policy

import (
	"strings"
	"testing"
)

// The generator names the build that wrote the policy file. It was a
// hard-coded "0.63.0" whatever build ran.
func TestDefaultPolicyGeneratorIsTheBuild(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })
	Version = "9.9.9"
	for name, content := range map[string]string{"public": DefaultPublicPolicyContent(), "private": DefaultPrivatePolicyContent()} {
		if !strings.Contains(strings.SplitN(content, "\n", 2)[0], `"generator":"polis-cli-go/9.9.9"`) {
			t.Errorf("%s default header = %q, want the build's generator", name, strings.SplitN(content, "\n", 2)[0])
		}
	}
	if strings.Contains(DefaultPublicPolicyContent(), "0.63.0") {
		t.Error("the old hard-coded version is still written")
	}
}
