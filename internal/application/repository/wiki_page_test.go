package repository

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupWikiPagesTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	requireExecSQLFile(t, sqlDB, "..", "..", "..", "migrations", "sqlite", "000005_wiki_runtime_tables.up.sql")
	return db
}

// makeWikiPage builds a minimal WikiPage suitable for insert. Title is
// derived from the slug so ORDER BY title ASC yields a predictable
// test ordering without callers having to spell out both fields.
func makeWikiPage(kbID, slug, pageType, status string) *types.WikiPage {
	title := slug
	if idx := strings.LastIndex(slug, "/"); idx >= 0 {
		title = slug[idx+1:]
	}
	return &types.WikiPage{
		ID:              uuid.New().String(),
		TenantID:        1,
		KnowledgeBaseID: kbID,
		Slug:            slug,
		Title:           title,
		PageType:        pageType,
		Status:          status,
		Content:         "body of " + slug,
		Summary:         "summary of " + slug,
		WikiPath:        pageType + "/" + title,
		Version:         1,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
}

func makeCategorizedWikiPage(kbID, slug, pageType, status string, categoryPath ...string) *types.WikiPage {
	page := makeWikiPage(kbID, slug, pageType, status)
	page.CategoryPath = types.StringArray(categoryPath)
	if len(categoryPath) > 0 {
		page.WikiPath = pageType + "/" + strings.Join(categoryPath, "/") + "/" + page.Title
		page.Depth = len(categoryPath)
	}
	return page
}

// TestList_WikiPathSortReturnsCategorizedPagesFirst protects the sidebar's
// IDE-like tree contract. Pagination happens in the repository, so the DB
// must return pages with category_path before loose root pages; otherwise the
// frontend cannot know about directories hiding on later pages.
func TestList_WikiPathSortReturnsCategorizedPagesFirst(t *testing.T) {
	db := setupWikiPagesTestDB(t)
	repo := NewWikiPageRepository(db)
	ctx := context.Background()

	pages := []*types.WikiPage{
		makeWikiPage("kb-a", "entity/000-root", types.WikiPageTypeEntity, types.WikiPageStatusPublished),
		makeCategorizedWikiPage("kb-a", "entity/999-child", types.WikiPageTypeEntity, types.WikiPageStatusPublished, "zzz-folder"),
		makeWikiPage("kb-a", "entity/001-root", types.WikiPageTypeEntity, types.WikiPageStatusPublished),
	}
	for _, p := range pages {
		require.NoError(t, repo.Create(ctx, p))
	}

	got, total, err := repo.List(ctx, &types.WikiPageListRequest{
		KnowledgeBaseID: "kb-a",
		PageType:        types.WikiPageTypeEntity,
		Page:            1,
		PageSize:        10,
		SortBy:          "wiki_path",
		SortOrder:       "asc",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, got, 3)
	assert.Equal(t, "entity/999-child", got[0].Slug)
	assert.Equal(t, "entity/000-root", got[1].Slug)
	assert.Equal(t, "entity/001-root", got[2].Slug)
}

// TestFolderTree_CRUDAndChildListing exercises the wiki_folders repository:
// child listing ordered by sort_order/name, find-by-name, page counting under
// a folder, and that ListDistinctCategoryPaths reflects the folder paths.
func TestFolderTree_CRUDAndChildListing(t *testing.T) {
	db := setupWikiPagesTestDB(t)
	repo := NewWikiPageRepository(db)
	ctx := context.Background()

	mk := func(id, parentID, name, path string, depth int) *types.WikiFolder {
		return &types.WikiFolder{
			ID: id, TenantID: 1, KnowledgeBaseID: "kb-f",
			ParentID: parentID, Name: name, Path: path, Depth: depth,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
	}
	ai := mk("f-ai", types.WikiFolderRootID, "AI", "AI", 1)
	people := mk("f-people", types.WikiFolderRootID, "人物", "人物", 1)
	llm := mk("f-llm", "f-ai", "LLM", "AI/LLM", 2)
	for _, f := range []*types.WikiFolder{ai, people, llm} {
		require.NoError(t, repo.CreateFolder(ctx, f))
	}

	// Root children: AI, 人物 (ordered by name within equal sort_order).
	roots, err := repo.ListChildFolders(ctx, "kb-f", types.WikiFolderRootID)
	require.NoError(t, err)
	require.Len(t, roots, 2)

	// Direct child of AI is LLM.
	child, err := repo.GetChildFolderByName(ctx, "kb-f", "f-ai", "LLM")
	require.NoError(t, err)
	assert.Equal(t, "f-llm", child.ID)

	// Missing child surfaces the typed not-found error.
	_, err = repo.GetChildFolderByName(ctx, "kb-f", "f-ai", "Nope")
	assert.ErrorIs(t, err, ErrWikiFolderNotFound)

	// Pages filed into folders are counted (archived excluded).
	pAI := makeCategorizedWikiPage("kb-f", "entity/a1", types.WikiPageTypeEntity, types.WikiPageStatusPublished, "AI")
	pAI.FolderID = "f-ai"
	pLLM := makeCategorizedWikiPage("kb-f", "entity/a2", types.WikiPageTypeEntity, types.WikiPageStatusPublished, "AI", "LLM")
	pLLM.FolderID = "f-llm"
	pArch := makeCategorizedWikiPage("kb-f", "entity/a3", types.WikiPageTypeEntity, types.WikiPageStatusArchived, "AI")
	pArch.FolderID = "f-ai"
	for _, p := range []*types.WikiPage{pAI, pLLM, pArch} {
		require.NoError(t, repo.Create(ctx, p))
	}

	aiCount, err := repo.CountPagesInFolder(ctx, "kb-f", "f-ai")
	require.NoError(t, err)
	assert.Equal(t, int64(1), aiCount, "archived page excluded; LLM page is under f-llm, not f-ai")

	// ListDistinctCategoryPaths returns the folder paths split into segments.
	paths, err := repo.ListDistinctCategoryPaths(ctx, "kb-f", 100)
	require.NoError(t, err)
	assert.Contains(t, paths, []string{"AI"})
	assert.Contains(t, paths, []string{"AI", "LLM"})
	assert.Contains(t, paths, []string{"人物"})

	// Pages can be fetched by their folder ids for subtree recompute.
	pages, err := repo.ListPagesByFolderIDs(ctx, "kb-f", []string{"f-ai", "f-llm"})
	require.NoError(t, err)
	assert.Len(t, pages, 3)
}

// TestListByTypeLight_ProjectsNarrowColumnsAndExcludesArchived verifies
// that the index-view projection only emits the slug/title/summary
// triples and respects the archived-status filter. This is the whole
// point of splitting the method off ListByType — the index reader must
// not pay for TEXT content transport.
func TestListByTypeLight_ProjectsNarrowColumnsAndExcludesArchived(t *testing.T) {
	db := setupWikiPagesTestDB(t)
	repo := NewWikiPageRepository(db)
	ctx := context.Background()

	pages := []*types.WikiPage{
		makeWikiPage("kb-a", "entity/alpha", types.WikiPageTypeEntity, types.WikiPageStatusPublished),
		makeWikiPage("kb-a", "entity/beta", types.WikiPageTypeEntity, types.WikiPageStatusDraft),
		makeWikiPage("kb-a", "entity/gamma", types.WikiPageTypeEntity, types.WikiPageStatusArchived),
		makeWikiPage("kb-a", "concept/delta", types.WikiPageTypeConcept, types.WikiPageStatusPublished),
		makeWikiPage("kb-other", "entity/leaked", types.WikiPageTypeEntity, types.WikiPageStatusPublished),
	}
	for _, p := range pages {
		require.NoError(t, repo.Create(ctx, p))
	}

	entries, total, err := repo.ListByTypeLight(ctx, "kb-a", types.WikiPageTypeEntity, 50, 0)
	require.NoError(t, err)

	// Archived is excluded; sibling KB is excluded; everything else
	// surfaces regardless of draft/published status (the index shows
	// both so admins notice newly-drafted pages).
	assert.Equal(t, int64(2), total)
	require.Len(t, entries, 2)

	// ORDER BY title ASC => alpha, beta.
	assert.Equal(t, "entity/alpha", entries[0].Slug)
	assert.Equal(t, "alpha", entries[0].Title)
	assert.Equal(t, "summary of entity/alpha", entries[0].Summary)
	assert.Equal(t, "entity/beta", entries[1].Slug)
}

// TestListByTypeLight_Pagination walks the type list using offsets and
// asserts the count stays stable regardless of where in the list we
// are — the index handler uses total to render "showing N of M".
func TestListByTypeLight_Pagination(t *testing.T) {
	db := setupWikiPagesTestDB(t)
	repo := NewWikiPageRepository(db)
	ctx := context.Background()

	for _, s := range []string{"entity/a", "entity/b", "entity/c", "entity/d", "entity/e"} {
		require.NoError(t, repo.Create(ctx, makeWikiPage("kb-a", s, types.WikiPageTypeEntity, types.WikiPageStatusPublished)))
	}

	page1, total1, err := repo.ListByTypeLight(ctx, "kb-a", types.WikiPageTypeEntity, 2, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total1)
	require.Len(t, page1, 2)
	assert.Equal(t, "entity/a", page1[0].Slug)
	assert.Equal(t, "entity/b", page1[1].Slug)

	page2, total2, err := repo.ListByTypeLight(ctx, "kb-a", types.WikiPageTypeEntity, 2, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total2, "total should be stable across pages")
	require.Len(t, page2, 2)
	assert.Equal(t, "entity/c", page2[0].Slug)
	assert.Equal(t, "entity/d", page2[1].Slug)

	page3, total3, err := repo.ListByTypeLight(ctx, "kb-a", types.WikiPageTypeEntity, 2, 4)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total3)
	require.Len(t, page3, 1)
	assert.Equal(t, "entity/e", page3[0].Slug)

	// Offset past the end yields an empty list, not an error — the
	// handler relies on this to short-circuit pagination tails.
	page4, total4, err := repo.ListByTypeLight(ctx, "kb-a", types.WikiPageTypeEntity, 2, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total4)
	assert.Empty(t, page4)
}

// TestListByTypeLight_EmptyType_ReturnsZero exercises the "no rows"
// short-circuit. We skip the SELECT entirely when count is zero, so
// a KB with no pages of a type shouldn't burn a pointless query.
func TestListByTypeLight_EmptyType_ReturnsZero(t *testing.T) {
	db := setupWikiPagesTestDB(t)
	repo := NewWikiPageRepository(db)
	ctx := context.Background()

	entries, total, err := repo.ListByTypeLight(ctx, "kb-empty", types.WikiPageTypeSynthesis, 50, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, entries)
}

// TestListByTypeLight_ClampsLimit verifies the [1, 200] clamp. We don't
// want a client passing limit=100000 and forcing the DB to return a
// multi-MB response.
func TestListByTypeLight_ClampsLimit(t *testing.T) {
	db := setupWikiPagesTestDB(t)
	repo := NewWikiPageRepository(db)
	ctx := context.Background()

	for i := 0; i < 250; i++ {
		slug := "entity/bulk-"
		// Use a stable, zero-padded suffix so title ordering is
		// deterministic for the cap assertion below.
		slug += string(rune('a'+(i/26)%26)) + string(rune('a'+i%26))
		require.NoError(t, repo.Create(ctx, makeWikiPage("kb-cap", slug, types.WikiPageTypeEntity, types.WikiPageStatusPublished)))
	}

	// limit=0 falls back to the default of 50.
	defaultEntries, _, err := repo.ListByTypeLight(ctx, "kb-cap", types.WikiPageTypeEntity, 0, 0)
	require.NoError(t, err)
	assert.Len(t, defaultEntries, 50)

	// limit=5000 clamps to 200.
	clampedEntries, _, err := repo.ListByTypeLight(ctx, "kb-cap", types.WikiPageTypeEntity, 5000, 0)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(clampedEntries), 200)
}

func TestWikiPageRepository_SourceRefsUseSQLiteJSONEach(t *testing.T) {
	db := setupWikiPagesTestDB(t)
	repo := NewWikiPageRepository(db)
	ctx := context.Background()

	exact := makeWikiPage("kb-src", "summary/exact", types.WikiPageTypeSummary, types.WikiPageStatusPublished)
	exact.SourceRefs = types.StringArray{"kid-exact"}
	exact.Content = "exact content"
	legacy := makeWikiPage("kb-src", "summary/legacy", types.WikiPageTypeSummary, types.WikiPageStatusPublished)
	legacy.SourceRefs = types.StringArray{"kid-legacy|Legacy Doc"}
	legacy.Content = "legacy content"
	archived := makeWikiPage("kb-src", "summary/archived", types.WikiPageTypeSummary, types.WikiPageStatusArchived)
	archived.SourceRefs = types.StringArray{"kid-legacy"}
	otherKB := makeWikiPage("kb-other", "summary/other", types.WikiPageTypeSummary, types.WikiPageStatusPublished)
	otherKB.SourceRefs = types.StringArray{"kid-legacy"}
	for _, p := range []*types.WikiPage{exact, legacy, archived, otherKB} {
		require.NoError(t, repo.Create(ctx, p))
	}

	pages, err := repo.ListBySourceRef(ctx, "kb-src", "kid-legacy")
	require.NoError(t, err)
	require.Len(t, pages, 2)
	assert.ElementsMatch(t, []string{"summary/legacy", "summary/archived"}, []string{pages[0].Slug, pages[1].Slug})

	slugs, err := repo.ListSlugsBySourceRef(ctx, "kb-src", "kid-exact")
	require.NoError(t, err)
	assert.Equal(t, []string{"summary/exact"}, slugs)

	summaries, err := repo.ListSummariesByKnowledgeIDs(ctx, "kb-src", []string{"kid-exact", "kid-legacy"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"kid-exact":  "exact content",
		"kid-legacy": "legacy content",
	}, summaries)
}

func TestWikiPageRepository_SQLiteSearchPaths(t *testing.T) {
	db := setupWikiPagesTestDB(t)
	repo := NewWikiPageRepository(db)
	ctx := context.Background()

	titleHit := makeWikiPage("kb-search", "entity/acme", types.WikiPageTypeEntity, types.WikiPageStatusPublished)
	titleHit.Title = "Quarterly Plan"
	titleHit.Content = "plain body"
	contentHit := makeWikiPage("kb-search", "entity/body", types.WikiPageTypeEntity, types.WikiPageStatusPublished)
	contentHit.Title = "Body Only"
	contentHit.Content = "mentions quarterly budget"
	archived := makeWikiPage("kb-search", "entity/archived", types.WikiPageTypeEntity, types.WikiPageStatusArchived)
	archived.Title = "Quarterly Archived"
	for _, p := range []*types.WikiPage{titleHit, contentHit, archived} {
		require.NoError(t, repo.Create(ctx, p))
	}

	listed, total, err := repo.List(ctx, &types.WikiPageListRequest{
		KnowledgeBaseID: "kb-search",
		Query:           "quarterly",
		Page:            1,
		PageSize:        10,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, listed, 3)

	found, err := repo.Search(ctx, "kb-search", "quarterly", 10)
	require.NoError(t, err)
	require.Len(t, found, 2)
	assert.Equal(t, "entity/acme", found[0].Slug, "title match should rank ahead of content match")
	assert.Equal(t, "entity/body", found[1].Slug)
}

func TestWikiPageRepository_SQLiteSimilarAndOrphanStats(t *testing.T) {
	db := setupWikiPagesTestDB(t)
	repo := NewWikiPageRepository(db)
	ctx := context.Background()

	entity := makeWikiPage("kb-stats", "entity/vector-search", types.WikiPageTypeEntity, types.WikiPageStatusPublished)
	entity.Title = "Vector Search"
	entity.InLinks = types.StringArray{}
	concept := makeWikiPage("kb-stats", "concept/vector-db", types.WikiPageTypeConcept, types.WikiPageStatusPublished)
	concept.Title = "Vector Database"
	concept.InLinks = types.StringArray{"entity/vector-search"}
	index := makeWikiPage("kb-stats", "index", types.WikiPageTypeIndex, types.WikiPageStatusPublished)
	index.Title = "Index"
	for _, p := range []*types.WikiPage{entity, concept, index} {
		require.NoError(t, repo.Create(ctx, p))
	}

	similar, err := repo.FindSimilarPages(ctx, "kb-stats", "vector", nil, 10)
	require.NoError(t, err)
	require.Len(t, similar, 2)
	assert.ElementsMatch(t, []string{"entity/vector-search", "concept/vector-db"}, []string{similar[0].Slug, similar[1].Slug})

	count, err := repo.CountOrphans(ctx, "kb-stats")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}
