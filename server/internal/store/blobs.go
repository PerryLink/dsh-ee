package store

import "context"

func (s *Store) BlobReferences(ctx context.Context) (map[string]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT blob_key FROM npm_versions
		UNION
		SELECT blob_key FROM package_source_files
		UNION
		SELECT blob_key FROM kb_docs WHERE blob_key <> ''
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := make(map[string]struct{})
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys[key] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}
