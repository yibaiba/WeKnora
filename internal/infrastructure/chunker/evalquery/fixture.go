package evalquery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func LoadFixture(path string) (Fixture, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Fixture{}, err
	}
	var fixture Fixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		return Fixture{}, fmt.Errorf("decode fixture: %w", err)
	}
	return fixture, nil
}

func MarshalFixture(fixture Fixture) ([]byte, error) {
	raw, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		return nil, err
	}
	if !bytes.HasSuffix(raw, []byte("\n")) {
		raw = append(raw, '\n')
	}
	return raw, nil
}

func WriteFixture(path string, fixture Fixture) error {
	raw, err := MarshalFixture(fixture)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

func ReadMarkdown(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(string(raw), "\uFEFF"), nil
}

func (r GenerateReport) Summary() string {
	return fmt.Sprintf(
		"accepted=%d attempts=%d rejected_invalid=%d rejected_compound=%d rejected_structure=%d rejected_duplicate=%d rejected_irrelevant=%d rejected_marker=%d",
		len(r.Accepted),
		r.Attempts,
		r.RejectedInvalid,
		r.RejectedCompound,
		r.RejectedStructure,
		r.RejectedDuplicate,
		r.RejectedIrrelevant,
		r.RejectedMarker,
	)
}
