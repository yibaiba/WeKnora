package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker/evalquery"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	if getenv == nil {
		getenv = os.Getenv
	}
	flags := flag.NewFlagSet("semantic-eval-gen", flag.ContinueOnError)
	flags.SetOutput(stderr)
	fixturePath := flags.String("fixture", "", "existing eval JSON to merge; structure queries are always kept")
	sourcePath := flags.String("source", "", "markdown to generate from (defaults to fixture markdown)")
	outPath := flags.String("out", "", "write merged JSON to this path; default is stdout")
	count := flags.Int("count", 4, "number of ordinary queries to accept")
	windowChars := flags.Int("window-chars", 0, "document window size in runes; 0 uses 4000")
	seed := flags.Int64("seed", 0, "window sampling seed; 0 uses the current time")
	excerptMin := flags.Float64("excerpt-min-cosine", 0, "minimum question-excerpt cosine; 0 uses 0.40")
	duplicateMax := flags.Float64("duplicate-max-cosine", 0, "maximum question-question cosine; 0 uses 0.70")
	replaceOrdinary := flags.Bool("replace-ordinary", false, "drop existing ordinary queries before appending generated ones")
	chatBase := flags.String("chat-base-url", getenv("SEMANTIC_EVAL_CHAT_BASE_URL"), "OpenAI-compatible chat base URL")
	chatKey := flags.String("chat-api-key", getenv("SEMANTIC_EVAL_CHAT_API_KEY"), "chat API key")
	chatModel := flags.String("chat-model", getenv("SEMANTIC_EVAL_CHAT_MODEL"), "chat model name")
	embedBase := flags.String("embed-base-url", getenv("SEMANTIC_EVAL_EMBEDDING_BASE_URL"), "OpenAI-compatible embedding base URL")
	embedKey := flags.String("embed-api-key", getenv("SEMANTIC_EVAL_EMBEDDING_API_KEY"), "embedding API key")
	embedModel := flags.String("embed-model", getenv("SEMANTIC_EVAL_EMBEDDING_MODEL"), "embedding model name")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*fixturePath) == "" && strings.TrimSpace(*sourcePath) == "" {
		return fmt.Errorf("provide -fixture and/or -source")
	}
	if strings.TrimSpace(*chatBase) == "" || strings.TrimSpace(*chatModel) == "" {
		return fmt.Errorf("chat endpoint required: set SEMANTIC_EVAL_CHAT_BASE_URL and SEMANTIC_EVAL_CHAT_MODEL")
	}

	var fixture evalquery.Fixture
	if strings.TrimSpace(*fixturePath) != "" {
		loaded, err := evalquery.LoadFixture(*fixturePath)
		if err != nil {
			return fmt.Errorf("load fixture: %w", err)
		}
		fixture = loaded
	}
	markdown := fixture.Markdown
	if strings.TrimSpace(*sourcePath) != "" {
		source, err := evalquery.ReadMarkdown(*sourcePath)
		if err != nil {
			return fmt.Errorf("read source: %w", err)
		}
		markdown = source
	}
	if strings.TrimSpace(markdown) == "" {
		return fmt.Errorf("markdown is empty")
	}

	req := evalquery.GenerateRequest{
		Markdown:           markdown,
		Existing:           fixture.Queries,
		Count:              *count,
		WindowChars:        *windowChars,
		ExcerptMinCosine:   *excerptMin,
		DuplicateMaxCosine: *duplicateMax,
		Seed:               *seed,
		Chat:               newOpenAIChat(*chatBase, *chatKey, *chatModel),
	}
	if req.Seed == 0 {
		req.Seed = time.Now().UnixNano()
	}
	if strings.TrimSpace(*embedBase) != "" && strings.TrimSpace(*embedModel) != "" {
		req.Embed = newOpenAIEmbedder(*embedBase, *embedKey, *embedModel)
	} else {
		fmt.Fprintln(stderr, "warning: embedding endpoint missing; skipping cosine duplicate/relevance filters")
	}

	report, err := evalquery.Generate(req)
	if err != nil {
		return err
	}
	fmt.Fprintln(stderr, report.Summary())
	if *replaceOrdinary && len(report.Accepted) == 0 {
		return fmt.Errorf("refusing to replace ordinary queries with empty generation")
	}
	if len(report.Accepted) == 0 {
		return fmt.Errorf("accepted 0 ordinary queries, want %d", *count)
	}

	out := fixture
	out.Markdown = markdown
	out.Queries = evalquery.Merge(fixture.Queries, report.Accepted, *replaceOrdinary)
	if missing := evalquery.MissingMarkers(out.Markdown, out.Queries); len(missing) > 0 {
		fmt.Fprintf(stderr, "warning: markers not in markdown: %s\n", strings.Join(missing, ", "))
	}
	raw, err := evalquery.MarshalFixture(out)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*outPath) != "" {
		if err := os.WriteFile(*outPath, raw, 0o644); err != nil {
			return err
		}
	} else if _, err := stdout.Write(raw); err != nil {
		return err
	}
	if len(report.Accepted) < *count {
		return fmt.Errorf("accepted %d ordinary queries, want %d", len(report.Accepted), *count)
	}
	return nil
}
