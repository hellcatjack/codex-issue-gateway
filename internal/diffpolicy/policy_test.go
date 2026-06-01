package diffpolicy

import "testing"

func TestDenylistBlocksDockerComposeAndEnv(t *testing.T) {
	result := Evaluate(Input{
		ChangedFiles: []string{"README.md", "docker-compose.yml", ".env"},
		DenyPaths:    []string{".env", ".env.*", "**/*secret*", "**/*token*", "docker-compose.yml"},
	})
	if result.Allowed || len(result.DeniedFiles) != 2 {
		t.Fatalf("result = %#v", result)
	}
}

func TestReviewRequiredFilesMarkSecurityReview(t *testing.T) {
	result := Evaluate(Input{
		ChangedFiles:        []string{".github/workflows/test.yml", "internal/authz/authz.go"},
		DenyPaths:           []string{".env"},
		ReviewRequiredPaths: []string{".github/workflows/**", "internal/authz/**"},
	})
	if !result.Allowed || !result.RequiresSecurityReview {
		t.Fatalf("result = %#v", result)
	}
}
