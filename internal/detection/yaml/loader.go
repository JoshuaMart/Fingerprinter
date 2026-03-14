package yaml

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	goyaml "gopkg.in/yaml.v3"
)

// LoadDir loads all *.yml files from the given directory (recursively) and returns validated definitions.
func LoadDir(dir string) ([]Definition, error) {
	var defs []Definition

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			return nil
		}

		def, err := loadFile(path)
		if err != nil {
			return fmt.Errorf("loading %s: %w", path, err)
		}
		defs = append(defs, *def)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking detections directory %s: %w", dir, err)
	}

	return defs, nil
}

func loadFile(path string) (*Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var def Definition
	if err := goyaml.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("parsing YAML: %w", err)
	}

	if err := validate(&def); err != nil {
		return nil, err
	}

	return &def, nil
}

func validate(def *Definition) error {
	if def.Name == "" {
		return fmt.Errorf("missing required field 'name'")
	}
	if def.Category == "" {
		return fmt.Errorf("missing required field 'category' in %q", def.Name)
	}
	return nil
}
