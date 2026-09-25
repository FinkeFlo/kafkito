package kafka

import (
	"testing"

	"github.com/antchfx/xpath"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin a contract that is otherwise invisible and spans the
// frontend/backend boundary: frontend/src/lib/xml-path-tree.ts builds XPath
// *suggestions* for the search UI from DOM `tagName`s, which keep any
// namespace prefix ("ns:order"). That is only correct because xmlquery — the
// engine that actually evaluates those paths here — matches prefixes
// literally and does not resolve them against xmlns declarations.
//
// Without this test, "normalizing" prefixes away in the suggestion builder
// would look like a cleanup and would silently produce paths that can never
// match, with nothing failing anywhere.
func evalXPath(t *testing.T, path, doc string) []string {
	t.Helper()
	expr, err := xpath.Compile(path)
	require.NoError(t, err)
	values, ok, err := xmlPathEval(expr)(doc)
	require.NoError(t, err)
	require.True(t, ok, "value should be recognized as XML")
	return values
}

func TestXPathMatchesNamespacePrefixesLiterally(t *testing.T) {
	const doc = `<ns:order xmlns:ns="urn:x" ns:id="7"><ns:status>shipped</ns:status></ns:order>`

	t.Run("prefixed element and attribute paths match", func(t *testing.T) {
		assert.Equal(t, []string{"shipped"}, evalXPath(t, "//ns:order/ns:status", doc))
		assert.Equal(t, []string{"7"}, evalXPath(t, "//ns:order/@ns:id", doc))
	})

	t.Run("stripping the prefix matches nothing", func(t *testing.T) {
		// The reason xml-path-tree.ts must not normalize prefixes away: the
		// local name alone is not a valid path into a prefixed document.
		assert.Empty(t, evalXPath(t, "//order/status", doc))
		assert.Empty(t, evalXPath(t, "//order/@id", doc))
	})
}

func TestXPathIgnoresDefaultNamespace(t *testing.T) {
	// A default xmlns carries no prefix, so DOM tagName is the bare local name
	// and xmlquery matches it unprefixed — the two agree here too, which is why
	// the suggestion builder needs no special handling for this case.
	const doc = `<order xmlns="urn:d" id="7"><status>shipped</status></order>`

	assert.Equal(t, []string{"shipped"}, evalXPath(t, "//order/status", doc))
	assert.Equal(t, []string{"7"}, evalXPath(t, "//order/@id", doc))
}
