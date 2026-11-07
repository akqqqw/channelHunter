package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/joho/godotenv"
)

// loadEnvFile reads a .env file and exports every key that is not already set
// in the process environment. Real environment variables always win so that
// container/deployment configuration is never overridden by a local file.
func loadEnvFile(path string) error {
	values, err := godotenv.Read(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("env file %q not found: %w", path, err)
		}
		return fmt.Errorf("read env file %q: %w", path, err)
	}
	for key, value := range values {
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("export %s from env file: %w", key, err)
		}
	}
	return nil
}
