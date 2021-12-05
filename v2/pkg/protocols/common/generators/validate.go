package generators

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/projectdiscovery/folderutil"
	"github.com/projectdiscovery/nuclei/v2/pkg/types"
)

// validate validates the payloads if any.
func (g *PayloadGenerator) validate(payloads map[string]interface{}, templatePath string) error {
	for name, payload := range payloads {
		switch payloadType := payload.(type) {
		case string:
			// check if it's a multiline string list
			if len(strings.Split(payloadType, "\n")) != 1 {
				return errors.New("invalid number of lines in payload")
			}

			// check if it's a worldlist file and try to load it
			if fileExists(payloadType) {
				continue
			}

			changed := false

			templatePathInfo, err := folderutil.NewPathInfo(templatePath)
			if err != nil {
				return err
			}
			payloadPathsToProbe, err := templatePathInfo.MeshWith(payloadType)
			if err != nil {
				return err
			}
			for _, payloadPath := range payloadPathsToProbe {
				if fileExists(payloadPath) {
					payloads[name] = payloadPath
					changed = true
					break
				}
			}
			if !changed {
				return fmt.Errorf("the %s file for payload %s does not exist or does not contain enough elements", payloadType, name)
			}
		case interface{}:
			loadedPayloads := types.ToStringSlice(payloadType)
			if len(loadedPayloads) == 0 {
				return fmt.Errorf("the payload %s does not contain enough elements", name)
			}
		default:
			return fmt.Errorf("the payload %s has invalid type", name)
		}
	}
	return nil
}

// fileExists checks if a file exists and is not a directory
func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	if info == nil {
		return false
	}
	return !info.IsDir()
}
