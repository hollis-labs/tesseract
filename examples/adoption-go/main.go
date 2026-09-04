// Command adoption-go is the compile-checked external-consumer example used by
// docs/guides/tesseract-adoption-and-v0.9-migration.md.
package main

import (
	"context"
	"log"
	"os"

	"github.com/hollis-labs/tesseract"
	"github.com/hollis-labs/tesseract/memory"
)

func main() {
	ctx := context.Background()
	root, err := os.MkdirTemp("", "tesseract-adoption-")
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if removeErr := os.RemoveAll(root); removeErr != nil {
			log.Printf("remove temporary Tesseract root: %v", removeErr)
		}
	}()

	db, err := tesseract.Open(ctx, tesseract.Config{RootDir: root})
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			log.Printf("close tesseract: %v", closeErr)
		}
	}()

	rev, err := db.WriteMemory(ctx, memory.WriteInput{
		Domain:     memory.DomainMemory,
		Namespace:  "user/demo/memory/decisions",
		MemoryKey:  "release.channel",
		Status:     memory.StatusCanonical,
		Author:     memory.Author{AgentID: "nanite"},
		Trigger:    memory.TriggerExplicit,
		SessionID:  "adoption-v0.9",
		Origin:     memory.OriginProject,
		Confidence: 0.95,
		Payload: memory.Payload{
			Summary: "Nanite consumes immutable Tesseract release tags.",
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	hits, err := db.RecallMemory(ctx, memory.RecallInput{
		Namespaces: []string{"user/demo/memory"},
		Query:      "which Tesseract version should Nanite consume?",
		Ranking:    memory.RankingRelevance,
		SearchMode: memory.SearchModeLexical,
		Limit:      5,
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s; recalled %d result(s)", rev.RevisionID, len(hits))
}
