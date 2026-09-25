package kafka

import (
	"testing"

	"github.com/ohler55/ojg/jp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin a contract that spans the frontend/backend boundary:
// frontend/src/lib/path-tree.ts builds JSONPath *suggestions* from sample
// messages, and for a record whose whole value is an array (a batch of rows
// per message — a normal Kafka shape) it emits paths prefixed with "$[*]".
//
// That prefix is not cosmetic. On a root-level array "$.field" matches
// nothing at all, so dropping the "[*]" — which looks like a harmless
// simplification — would produce suggestions that can never match, with
// nothing failing anywhere.
func evalJSONPath(t *testing.T, path, doc string) []string {
	t.Helper()
	expr, err := jp.ParseString(path)
	require.NoError(t, err)
	values, ok, err := jsonPathEval(expr)(doc)
	require.NoError(t, err)
	require.True(t, ok, "value should be recognized as JSON")
	return values
}

func TestJSONPathRequiresWildcardForRootArrays(t *testing.T) {
	const doc = `[{"RUNID":"abc","meta":{"step":1}},{"RUNID":"def","meta":{"step":2}}]`

	assert.Equal(t, []string{"abc", "def"}, evalJSONPath(t, "$[*].RUNID", doc),
		"the wildcard form is what the suggestion builder emits")
	assert.Equal(t, []string{"1", "2"}, evalJSONPath(t, "$[*].meta.step", doc),
		"nested fields keep working under the wildcard prefix")

	assert.Empty(t, evalJSONPath(t, "$.RUNID", doc),
		"dropping the wildcard silently matches nothing on a root array")
}

func TestJSONPathRootObjectsKeepTheDotForm(t *testing.T) {
	const doc = `{"RUNID":"abc","meta":{"step":1}}`

	assert.Equal(t, []string{"abc"}, evalJSONPath(t, "$.RUNID", doc))
	assert.Empty(t, evalJSONPath(t, "$[*].RUNID", doc),
		"the wildcard form must not be emitted for root objects")
}
