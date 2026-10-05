package registry

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

// IAM validates a role description against this expression: tab, LF, CR,
// printable ASCII, and the Latin-1 supplement. It stops at U+00FF, so the
// typographic dashes and quotes that read well in prose are rejected —
// and the rejection names the REGEX, not the character, several layers
// down inside a Pulumi provider error at deploy time.
//
// KMS, by contrast, accepts arbitrary UTF-8 (esoiam's SDK test key
// carries an em dash and deployed fine), which is exactly why this is
// easy to get wrong: the same-looking string is valid in one resource
// and fatal in another.
var iamDescriptionRE = regexp.MustCompile(`^[\x09\x0A\x0D\x20-\x7E\xA1-\xFF]*$`)

func TestRoleDescriptionsAreIAMSafe(t *testing.T) {
	t.Parallel()

	for name, description := range map[string]string{
		"codeartifact reader": fmt.Sprintf(codeArtifactRoleDescription, "app"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.True(t, iamDescriptionRE.MatchString(description),
				"IAM rejects this description at CreateRole: %q", description)
		})
	}
}
