package migrate

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/PatchMon/PatchMon/server-source-code/internal/forkschema"
)

// forkSchemaTimeout bounds the fork's schema steps. They are small, but an
// ALTER TABLE waits for the locks other sessions hold on the table.
const forkSchemaTimeout = time.Minute

// applyForkSchema runs this fork's schema steps right after the migrations,
// on the same database: they are not numbered migrations so that upstream's
// keep applying (see internal/forkschema).
func applyForkSchema(databaseURL string, log *slog.Logger) error {
	ctx, cancel := context.WithTimeout(context.Background(), forkSchemaTimeout)
	defer cancel()
	if err := forkschema.ApplyURL(ctx, databaseURL, log); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "[migrate] fork schema steps failed: %v\n", err)
		return fmt.Errorf("fork schema: %w", err)
	}
	return nil
}
