package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	queuesqlite "github.com/hollis-labs/go-queue/driver/sqlite"

	conduit "github.com/hollis-labs/tesseract"
	llmopenai "github.com/hollis-labs/tesseract/internal/llm/openai"
	"github.com/hollis-labs/tesseract/internal/memory"
	_ "modernc.org/sqlite"
)

func main() {
	ctx := context.Background()

	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".tesseract")

	queueDSN := fmt.Sprintf("file:%s?_busy_timeout=5000&_fk=1", filepath.Join(root, "data", "queue.db"))
	qdb, err := sql.Open("sqlite", queueDSN)
	if err != nil {
		log.Fatalf("open queue db: %v", err)
	}
	defer qdb.Close()
	q, err := queuesqlite.New(qdb, queuesqlite.Opts{})
	if err != nil {
		log.Fatalf("queue driver: %v", err)
	}

	c, err := conduit.Open(ctx, conduit.Config{RootDir: root},
		conduit.WithQueue(q),
		conduit.WithEmbedder(llmopenai.New("")),
		conduit.WithEmbeddingModel("text-embedding-3-large"),
	)
	if err != nil {
		log.Fatalf("conduit.Open: %v", err)
	}
	defer c.Close()

	ns := "user/chrispian/project/conduit/memory"
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
				fmt.Printf("  - ns=%s key=%s score=%.4f summary=%q\n", r.Revision.Namespace, r.Revision.MemoryKey, r.Score, r.Revision.Payload.Summary)
			}
			return
		}
	}
	log.Fatalf("timed out waiting for embedding on %s", rev.RevisionID)
}
