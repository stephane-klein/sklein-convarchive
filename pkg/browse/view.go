package browse

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"filippo.io/age"
	"github.com/charmbracelet/bubbletea"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/stephane-klein/sklein-convarchive/pkg/archive"
)

// Config holds everything the browser needs to run.
type Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool

	// AgeIdentity is an age private key: either a path to a key file, or the
	// raw key content ("AGE-SECRET-KEY-1..."). Required to read encrypted
	// (.age) objects.
	AgeIdentity string
}

// LoadIdentity parses the age identity from value. value may be the raw secret
// key content ("AGE-SECRET-KEY-1...") or a path to a file holding it. It
// returns (nil, nil) when value is empty.
func LoadIdentity(value string) (age.Identity, error) {
	if value == "" {
		return nil, nil
	}
	if strings.HasPrefix(value, "AGE-SECRET-KEY-") {
		id, err := age.ParseX25519Identity(value)
		if err != nil {
			return nil, fmt.Errorf("invalid age identity: %w", err)
		}
		return id, nil
	}
	f, err := os.Open(value)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	ids, err := age.ParseIdentities(f)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, errors.New("no age identity found in " + value)
	}
	return ids[0], nil
}

// Run starts the TUI browser and returns when it exits. out/in allow testing
// with non-terminal streams.
func Run(cfg Config, stdin io.Reader, stdout io.Writer) error {
	identity, err := LoadIdentity(cfg.AgeIdentity)
	if err != nil {
		return err
	}

	client, err := minio.New(archive.NormalizeEndpoint(cfg.Endpoint), &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return err
	}

	reader := NewReader(&minioStore{client: client}, cfg.Bucket, identity)

	// The tree is loaded asynchronously by the model (Init fires a refresh
	// command), so the TUI shows a spinner while the listing runs.
	model := NewModel(reader, nil)
	program := tea.NewProgram(model, tea.WithInput(stdin), tea.WithOutput(stdout), tea.WithAltScreen())
	_, err = program.Run()
	return err
}
