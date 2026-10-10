package modelcatalog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAttributeSupplementPreservesCatalog 检查统一补充中的属性按字段补缺，价格由计费解析器处理。
func TestAttributeSupplementPreservesCatalog(t *testing.T) {
	catalog, err := Parse([]byte(`{"providers":{"typesafe":{"models":{"jev-preview":{"name":"Catalog preview","modalities":{"input":[]},"structured_output":false,"cost":{"input":0,"output":0}}}}}}`))
	require.NoError(t, err)
	supplement := []byte(`{"provider":"typesafe","input_cost_per_token":0.5,"attributes":{"display_name":"Supplement preview","input_modalities":["text"],"output_modalities":["text"],"structured_output":true}}`)
	require.NoError(t, catalog.ApplyAttributeSupplement("jev-preview", supplement))
	attrs, found := catalog.LookupAttributes("jev-preview", nil)
	require.True(t, found)
	require.Empty(t, *attrs.InputModalities)
	require.False(t, *attrs.StructuredOutput)
	require.Equal(t, "Catalog preview", *attrs.DisplayName)
	require.Equal(t, []string{"text"}, *attrs.OutputModalities)
	require.Zero(t, *catalog.Entries["jev-preview"].Cost.Input)
}

// TestAttributeSupplementRejectsInvalidData 检查非法属性和渠道身份冲突。
func TestAttributeSupplementRejectsInvalidData(t *testing.T) {
	catalog, err := Parse([]byte(`{"providers":{"relay":{"models":{"jev-preview":{"modalities":{"input":["image"]}}}}}}`))
	require.NoError(t, err)
	require.NoError(t, catalog.ApplyAttributeSupplement("relay/jev-preview", []byte(`{"provider":"typesafe","attributes":{"input_modalities":["text"],"output_modalities":["text"]}}`)))
	attrs, found := catalog.LookupAttributes("relay/jev-preview", nil)
	require.True(t, found)
	require.Equal(t, []string{"image"}, *attrs.InputModalities)
	require.Nil(t, attrs.OutputModalities)
	count := len(catalog.Entries)
	require.Error(t, catalog.ApplyAttributeSupplement("bad-model", []byte(`{"attributes":{"input_modalities":["invalid"]}}`)))
	require.Len(t, catalog.Entries, count)
}

func TestAttributeMergeAndCommon(t *testing.T) {
	yes, no := true, false
	context, smaller := 100, 50
	modalities, empty := []string{"text", "image", "pdf"}, []string{}
	base := Attributes{Context: &context, Reasoning: &yes, InputModalities: &modalities}
	patched := Merge(base, Attributes{Reasoning: &no, InputModalities: &empty})
	require.Equal(t, 100, *patched.Context)
	require.False(t, *patched.Reasoning)
	require.NotNil(t, patched.InputModalities)
	require.Empty(t, *patched.InputModalities)
	common, different := Common([]Attributes{base, {Context: &smaller, Reasoning: &no}})
	require.True(t, different)
	require.Equal(t, 50, *common.Context)
	require.False(t, *common.Reasoning)
	require.Nil(t, common.InputModalities)
}
