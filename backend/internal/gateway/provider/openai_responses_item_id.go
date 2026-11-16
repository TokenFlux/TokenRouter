package provider

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	openaiprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_responses_item_id.go removes rejected replay identifiers while
// retaining input items and tool-call associations.
// Responses replay has distinct item-ID and call-ID namespaces that can
// contain equal strings. A rejected item ID should not erase an output paired
// through call_id or an opaque item reference; sanitation therefore changes
// fields on individual items while keeping every item in the array.
//
// Classification records which fields need removal using the upstream
// type-specific rules. Reconstruction edits each raw item and replaces the
// complete input array only after those edits succeed. Unaffected field
// representations remain in raw item bytes, and bodies with no edits return
// unchanged. Callers receive nil on a deletion or final replacement error
// rather than a partially assembled body.

// SanitizeOpenAIResponsesInputItemIDs removes known invalid item IDs and
// non-pair call IDs from array input. Other input shapes or arrays requiring
// no cleanup return the original body with false; edit errors return nil and
// false.
//
// Each item is classified before reconstruction, then only its rejected fields
// are deleted from raw JSON. Item IDs and paired call IDs have separate
// namespaces, so removing an invalid item ID does not delete outputs or
// references bearing the same text. Raw item bytes retain their other values,
// and the rebuilt array replaces input after all item edits succeed.
func SanitizeOpenAIResponsesInputItemIDs(body []byte) ([]byte, bool, error) {
	input := wirejson.ParseView(body).Get("input")
	if !input.IsArray() {
		return body, false, nil
	}

	// dirtyItem records an input array index and the identity fields requiring deletion.
	type dirtyItem struct {
		index       int  // zero-based position in input
		stripID     bool // item identity requires removal
		stripCallID bool // non-pair call identity removal
	}

	var dirty []dirtyItem
	scanIndex := 0
	input.ForEach(func(_, item gjson.Result) bool {
		current := scanIndex
		scanIndex++
		if !item.IsObject() {
			return true
		}
		itemType := item.Get("type")
		id := item.Get("id")
		trimmedItemType := strings.TrimSpace(itemType.String())
		stripCallID := item.Get("call_id").Exists() && openaiprotocol.ShouldStripNonPairCallID(trimmedItemType)
		stripID := false
		if id.Type == gjson.String {
			stripID = openai.ShouldStripOpenAIResponsesInputItemID(trimmedItemType, id.String())
		}
		// Record only items needing edits so clean multi-megabyte histories return
		// immediately without allocating per-item byte copies or a slice per turn.
		if stripID || stripCallID {
			dirty = append(dirty, dirtyItem{
				index:       current,
				stripID:     stripID,
				stripCallID: stripCallID,
			})
		}
		return true
	})
	if len(dirty) == 0 {
		return body, false, nil
	}

	// Edit dirty objects independently while copying untouched raw items directly
	// into rebuiltInput, keeping call_id and item ID namespaces isolated.
	rebuiltInput := make([]byte, 0, len(input.Raw))
	rebuiltInput = append(rebuiltInput, '[')
	dirtyPos := 0
	itemIndex := 0
	var editErr error
	input.ForEach(func(_, item gjson.Result) bool {
		if itemIndex > 0 {
			rebuiltInput = append(rebuiltInput, ',')
		}
		if dirtyPos < len(dirty) && dirty[dirtyPos].index == itemIndex {
			target := dirty[dirtyPos]
			dirtyPos++
			itemBody := []byte(item.Raw)
			if target.stripID {
				var err error
				itemBody, err = sjson.DeleteBytes(itemBody, "id")
				if err != nil {
					editErr = fmt.Errorf("delete input.%d.id: %w", itemIndex, err)
					return false
				}
			}
			if target.stripCallID {
				var err error
				itemBody, err = sjson.DeleteBytes(itemBody, "call_id")
				if err != nil {
					editErr = fmt.Errorf("delete input.%d.call_id: %w", itemIndex, err)
					return false
				}
			}
			rebuiltInput = append(rebuiltInput, itemBody...)
		} else {
			rebuiltInput = append(rebuiltInput, item.Raw...)
		}
		itemIndex++
		return true
	})
	if editErr != nil {
		return nil, false, editErr
	}
	rebuiltInput = append(rebuiltInput, ']')

	// Replace input after every requested deletion succeeds; return no partial body
	// when final replacement fails, leaving caller adoption conditional on the error result.
	sanitized, err := sjson.SetRawBytes(body, "input", rebuiltInput)
	if err != nil {
		return nil, false, fmt.Errorf("replace sanitized input: %w", err)
	}
	return sanitized, true, nil
}
