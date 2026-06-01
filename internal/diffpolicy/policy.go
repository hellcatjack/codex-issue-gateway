package diffpolicy

import (
	"path/filepath"
	"strings"
)

type Input struct {
	ChangedFiles        []string
	DenyPaths           []string
	ReviewRequiredPaths []string
	MaxFiles            int
	MaxDeletedFiles     int
}

type Result struct {
	Allowed                bool
	DeniedFiles            []string
	ReviewFiles            []string
	RequiresSecurityReview bool
	Reason                 string
}

func Evaluate(in Input) Result {
	result := Result{Allowed: true}
	for _, file := range in.ChangedFiles {
		if matchesAny(file, in.DenyPaths) {
			result.DeniedFiles = append(result.DeniedFiles, file)
		}
		if matchesAny(file, in.ReviewRequiredPaths) {
			result.ReviewFiles = append(result.ReviewFiles, file)
		}
	}
	if len(result.DeniedFiles) > 0 {
		result.Allowed = false
		result.Reason = "diff_policy_failed"
	}
	if len(result.ReviewFiles) > 0 {
		result.RequiresSecurityReview = true
	}
	return result
}

func matchesAny(file string, patterns []string) bool {
	file = filepath.ToSlash(file)
	for _, pattern := range patterns {
		pattern = filepath.ToSlash(pattern)
		if ok, _ := filepath.Match(pattern, file); ok {
			return true
		}
		if strings.HasSuffix(pattern, "/**") {
			prefix := strings.TrimSuffix(pattern, "/**")
			if file == prefix || strings.HasPrefix(file, prefix+"/") {
				return true
			}
		}
	}
	return false
}
