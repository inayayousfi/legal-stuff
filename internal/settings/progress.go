package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
)

// Progress records which guided setup steps are finished, so an interrupted
// setup resumes where it stopped.
type Progress struct {
	path string
	done map[string]bool
}

func ReadProgress(path string) (*Progress, error) {
	progress := &Progress{path: path, done: map[string]bool{}}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return progress, nil
	}
	if err != nil {
		return nil, fmt.Errorf("Cannot read setup progress from %s.", path)
	}
	var steps []string
	if json.Unmarshal(content, &steps) != nil {
		var probe any
		if json.Unmarshal(content, &probe) != nil {
			return nil, fmt.Errorf("Cannot read setup progress from %s.", path)
		}
		return nil, fmt.Errorf("Invalid setup progress in %s.", path)
	}
	for _, step := range steps {
		progress.done[step] = true
	}
	return progress, nil
}

func (p *Progress) Done(step string) bool { return p.done[step] }

// Complete marks step as finished and saves the progress file.
func (p *Progress) Complete(step string) error {
	p.done[step] = true
	steps := make([]string, 0, len(p.done))
	for name := range p.done {
		steps = append(steps, name)
	}
	slices.Sort(steps)
	content, err := json.MarshalIndent(steps, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(p.path, append(content, '\n'), 0o644)
}
