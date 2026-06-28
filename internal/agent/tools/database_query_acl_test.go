package tools

import "testing"

func TestRejectDatabaseQueryContentFields(t *testing.T) {
	tests := []struct {
		name    string
		sql     string
		wantErr bool
	}{
		{
			name:    "blocks chunk content",
			sql:     "SELECT content FROM chunks LIMIT 1",
			wantErr: true,
		},
		{
			name:    "blocks wildcard on chunks",
			sql:     "SELECT * FROM chunks LIMIT 1",
			wantErr: true,
		},
		{
			name:    "blocks alias wildcard on chunks",
			sql:     "SELECT c.* FROM chunks c LIMIT 1",
			wantErr: true,
		},
		{
			name:    "blocks knowledge summary",
			sql:     "SELECT k.summary FROM knowledges k LIMIT 1",
			wantErr: true,
		},
		{
			name:    "blocks content in function argument",
			sql:     "SELECT length(c.content) FROM chunks c LIMIT 1",
			wantErr: true,
		},
		{
			name:    "allows knowledge metadata columns",
			sql:     "SELECT id, title, file_name FROM knowledges LIMIT 10",
			wantErr: false,
		},
		{
			name:    "allows knowledge base description",
			sql:     "SELECT id, description FROM knowledge_bases LIMIT 10",
			wantErr: false,
		},
		{
			name:    "allows aggregate over chunks",
			sql:     "SELECT count(*) FROM chunks",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rejectDatabaseQueryContentFields(tt.sql)
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
