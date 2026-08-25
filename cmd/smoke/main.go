package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"path/filepath"
	"time"

	queuesqlite "github.com/hollis-labs/go-queue/driver/sqlite"

	tesseract "github.com/hollis-labs/tesseract"
	"github.com/hollis-labs/tesseract/internal/config"
	llmopenai "github.com/hollis-labs/tesseract/internal/llm/openai"
	"github.com/hollis-labs/tesseract/internal/memory"
	"github.com/hollis-labs/tesseract/internal/sqlitedsn"
	_ "modernc.org/sqlite"
)

func main() {
	ctx := context.Background()

	// Resolve the go-apppaths layout so the smoke harness targets the same
	// XDG locations as the contextd daemon (CW-20260517-0066).
	layout, err := config.ResolveLayout()
	if err != nil {
		log.Fatalf("resolve layout: %v", err)
	}

	qdb, err := sql.Open("sqlite", sqlitedsn.DSN(filepath.Join(layout.StateDir(), "queue.db")))
	if err != nil {
		log.Fatalf("open queue db: %v", err)
	}
	defer qdb.Close()
	q, err := queuesqlite.New(qdb, queuesqlite.Opts{})
	if err != nil {
		log.Fatalf("queue driver: %v", err)
	}

	c, err := tesseract.Open(ctx, tesseract.Config{
		RootDir:    layout.DataDir(),
		DBPath:     layout.MainDB(),
		RecordsDir: filepath.Join(layout.StateDir(), "records"),
	},
		tesseract.WithQueue(q),
		tesseract.WithEmbedder(llmopenai.New("")),
		tesseract.WithEmbeddingModel("text-embedding-3-large"),
	)
	if err != nil {
		log.Fatalf("tesseract.Open: %v", err)
	}
	defer c.Close()

	ns := "user/chrispian/project/tesseract/memory"
	key := fmt.Sprintf("smoke_%d", time.Now().UnixNano())
	rev, err := c.WriteMemory(ctx, memory.WriteInput{
		Namespace:  ns,
		MemoryKey:  key,
		Status:     memory.StatusCanonical,
		Author:     memory.Author{AgentID: "claude-code", AgentVersion: "1.0"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "smoke-session",
		Origin:     memory.OriginObservation,
		Confidence: 0.9,
		Tags:       []string{"smoke"},
		Payload:    memory.Payload{Summary: "Tesseract embedding smoke test.", Body: "This is a short body for embedding. The Crow (1994) is a movie."},
	})
	if err != nil {
		log.Fatalf("WriteMemory: %v", err)
	}
	fmt.Printf("wrote revision=%s memory_id=%s\n", rev.RevisionID, rev.MemoryID)

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		got, err := c.MemoryStore().GetRevisionByID(ctx, rev.RevisionID)
		if err == nil && got.EmbeddingModel != "" && len(got.EmbeddingVector) > 0 {
			fmt.Printf("embedded: model=%s dim=%d\n", got.EmbeddingModel, len(got.EmbeddingVector))

			res, err := c.RecallMemory(ctx, memory.RecallInput{
				Namespaces: []string{ns},
				Ranking:    memory.RankingSimilarity,
				Query:      "the crow film",
				Limit:      3,
			})
			if err != nil {
				log.Fatalf("RecallMemory: %v", err)
			}
			fmt.Printf("recall similarity returned %d result(s)\n", len(res))
			for _, r := range res {
				score := "n/a"
				if r.Score != nil {
					score = fmt.Sprintf("%.4f", *r.Score)
				}
				fmt.Printf("  - ns=%s key=%s score=%s summary=%q\n", r.Revision.Namespace, r.Revision.MemoryKey, score, r.Revision.Payload.Summary)
			}
			return
		}
	}
	log.Fatalf("timed out waiting for embedding on %s", rev.RevisionID)
}
