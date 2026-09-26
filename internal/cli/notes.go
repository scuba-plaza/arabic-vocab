package cli

import (
	"fmt"

	"github.com/scuba-plaza/arabic-vocab/internal/notes"
	"github.com/scuba-plaza/arabic-vocab/internal/rank"
)

func loadRecords(path string) ([]rank.Record, error) {
	records, err := notes.ReadJSONL[rank.Record](path)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("%s is empty; run 'arabic-vocab rank' first", path)
	}
	return records, nil
}

func loadNotes(path string) ([]notes.Note, error) {
	ns, err := notes.ReadJSONL[notes.Note](path)
	if err != nil {
		return nil, err
	}
	if len(ns) == 0 {
		return nil, fmt.Errorf("%s has no notes; run 'arabic-vocab add' first", path)
	}
	if err := notes.Validate(ns); err != nil {
		return nil, err
	}
	notes.Sort(ns)
	return ns, nil
}
