package library

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const transferMarker = ".transfer-in-progress"
const restorePause = ".restore-paused"
const CurrentSchema = 18

type BackupFile struct {
	Path string `json:"path"`
	Hash string `json:"sha256"`
	Size int64  `json:"bytes"`
}
type BackupManifest struct {
	Format    int          `json:"format"`
	Schema    int          `json:"schema"`
	CreatedAt string       `json:"created_at"`
	Files     []BackupFile `json:"files"`
}

func readonlyDB(path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}
func regularPath(root, relative string) (string, error) {
	clean := filepath.Clean(relative)
	if filepath.IsAbs(relative) || clean != relative || clean == "." || strings.HasPrefix(clean, "..") {
		return "", errors.New("invalid backup path")
	}
	path := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("backup must not follow symlinks")
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("backup source is not a regular file")
	}
	return path, nil
}
func primaryRefs(db *sql.DB, schema int) (map[string]string, error) {
	refs := map[string]string{}
	if schema < 3 {
		return refs, nil
	} // Earlier schemas keep primary text inside SQLite.
	for _, ref := range primaryTables(schema) {
		query := "SELECT " + ref.column + ",hash FROM " + ref.table
		if ref.column == "response_path" {
			query += " WHERE response_path<>''"
		}
		rows, err := db.Query(query)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var path, hash string
			if err = rows.Scan(&path, &hash); err != nil {
				break
			}
			bytes, e := hex.DecodeString(hash)
			if e != nil || len(bytes) != sha256.Size || strings.ToLower(hash) != hash {
				err = errors.New("invalid primary hash")
				break
			}
			kind, extension := "digests", ".md"
			if ref.table == "source_versions" {
				kind, extension = "texts", ".txt"
			}
			expected, _ := blobPath(kind, hash) // Hash and kind were validated above.
			legacy := filepath.Join(kind, hash+extension)
			if path != expected && path != legacy {
				err = errors.New("invalid primary reference")
				break
			}
			if previous, ok := refs[path]; ok && previous != hash {
				err = errors.New("inconsistent primary reference")
				break
			}
			refs[path] = hash
		}
		rowErr := rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if rowErr != nil {
			return nil, rowErr
		}
	}
	return refs, nil
}
func dbIntegrity(db *sql.DB) (int, error) {
	var schema int
	if err := db.QueryRow("PRAGMA user_version").Scan(&schema); err != nil {
		return 0, err
	}
	if schema < 1 || schema > CurrentSchema {
		return 0, errors.New("unsupported backup schema")
	}
	var check string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil {
		return 0, err
	}
	if check != "ok" {
		return 0, errors.New("backup database integrity failed")
	}
	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		return 0, err
	}
	invalid := rows.Next()
	rowErr := rows.Err()
	rows.Close()
	if rowErr != nil {
		return 0, rowErr
	}
	if invalid {
		return 0, errors.New("backup database foreign keys failed")
	}
	return schema, nil
}
func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	return fmt.Sprintf("%x", hash.Sum(nil)), size, err
}
func syncedWrite(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	return err
}
func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func reserveTransfer(destination string) (string, error) {
	destination, err := filepath.Abs(destination)
	if err != nil {
		return "", err
	}
	if err = os.Mkdir(destination, 0700); err != nil {
		return "", fmt.Errorf("destination must not exist: %w", err)
	}
	if err = syncedWrite(filepath.Join(destination, transferMarker), []byte("Incomplete transfer; do not start the service here.\n")); err != nil {
		return "", err
	}
	if err = syncDirectory(destination); err != nil {
		return "", err
	}
	if err = syncDirectory(filepath.Dir(destination)); err != nil {
		return "", err
	}
	return destination, nil
}
func transferFile(source, destination, relative, hash string) (BackupFile, error) {
	return transferFileAs(source, destination, relative, relative, hash)
}
func transferFileAs(source, destination, relative, targetRelative, hash string) (BackupFile, error) {
	path, err := regularPath(source, relative)
	if err != nil {
		return BackupFile{}, err
	}
	target := filepath.Join(destination, targetRelative)
	if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return BackupFile{}, err
	}
	input, err := os.Open(path)
	if err != nil {
		return BackupFile{}, err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return BackupFile{}, err
	}
	sum := sha256.New()
	size, err := io.Copy(io.MultiWriter(output, sum), input)
	if err == nil {
		err = output.Sync()
	}
	closeErr := output.Close()
	if err == nil {
		err = closeErr
	}
	actual := fmt.Sprintf("%x", sum.Sum(nil))
	if err != nil {
		return BackupFile{}, err
	}
	if actual != hash {
		return BackupFile{}, errors.New("primary file hash mismatch")
	}
	// Every shard directory entry must be durable, not just the leaf file.
	for dir := filepath.Dir(target); ; dir = filepath.Dir(dir) {
		if err = syncDirectory(dir); err != nil {
			return BackupFile{}, err
		}
		if dir == filepath.Clean(destination) || dir == filepath.Dir(dir) {
			break
		}
	}
	return BackupFile{Path: targetRelative, Hash: hash, Size: size}, nil
}

// Backup captures a consistent live SQLite snapshot without migrating or
// modifying source metadata, then copies immutable files referenced by it.
func Backup(ctx context.Context, source, destination string) (BackupManifest, error) {
	var manifest BackupManifest
	source, err := filepath.Abs(source)
	if err != nil {
		return manifest, err
	}
	dbPath, err := regularPath(source, "library.sqlite")
	if err != nil {
		return manifest, err
	}
	output, err := reserveTransfer(destination)
	if err != nil {
		return manifest, err
	}
	// Failed destinations intentionally retain an incomplete marker; never
	// recursively delete an output directory that another process could use.
	db, err := readonlyDB(dbPath)
	if err != nil {
		return manifest, err
	}
	defer db.Close()
	snapshot := filepath.Join(output, "database.snapshot.tmp")
	if _, err = db.ExecContext(ctx, "VACUUM INTO ?", snapshot); err != nil {
		return manifest, err
	}
	if err = os.Chmod(snapshot, 0600); err != nil {
		return manifest, err
	}
	frozen, err := readonlyDB(snapshot)
	if err != nil {
		return manifest, err
	}
	schema, err := dbIntegrity(frozen)
	var refs map[string]string
	if err == nil {
		refs, err = primaryRefs(frozen, schema)
	}
	frozen.Close()
	if err != nil {
		return manifest, err
	}
	manifest = BackupManifest{Format: 1, Schema: schema, CreatedAt: nowUTC(), Files: []BackupFile{}}
	paths := make([]string, 0, len(refs))
	for path := range refs {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if ctx.Err() != nil {
			return manifest, ctx.Err()
		}
		file, e := transferFile(source, output, path, refs[path])
		if e != nil {
			return manifest, e
		}
		manifest.Files = append(manifest.Files, file)
	}
	sum, size, err := hashFile(snapshot)
	if err != nil {
		return manifest, err
	}
	manifest.Files = append(manifest.Files, BackupFile{Path: "library.sqlite", Hash: sum, Size: size})
	// Exclusive publication plus the incomplete marker never overwrites data
	// and works without filesystem hard-link support.
	if _, err = transferFileAs(output, output, "database.snapshot.tmp", "library.sqlite", sum); err != nil {
		return manifest, err
	}

	if err = os.Remove(snapshot); err != nil {
		return manifest, err
	}
	encoded, _ := json.MarshalIndent(manifest, "", "  ")
	if err = syncedWrite(filepath.Join(output, "backup.json"), encoded); err != nil {
		return manifest, err
	}
	if err = syncDirectory(output); err != nil {
		return manifest, err
	}
	if err = os.Remove(filepath.Join(output, transferMarker)); err != nil {
		return manifest, err
	}
	return manifest, syncDirectory(output)
}
func VerifyBackup(source string) (BackupManifest, error) {
	var manifest BackupManifest
	source, err := filepath.Abs(source)
	if err != nil {
		return manifest, err
	}
	if _, err = os.Lstat(filepath.Join(source, transferMarker)); !errors.Is(err, os.ErrNotExist) {
		return manifest, errors.New("incomplete backup")
	}
	path, err := regularPath(source, "backup.json")
	if err != nil {
		return manifest, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return manifest, err
	}
	if info.Size() > 16<<20 {
		return manifest, errors.New("backup manifest too large")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest, err
	}
	if len(data) > 16<<20 {
		return manifest, errors.New("backup manifest too large")
	}
	if err = json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	if manifest.Format != 1 || manifest.Schema < 1 || manifest.Schema > CurrentSchema {
		return manifest, errors.New("unsupported backup format")
	}
	listed := map[string]BackupFile{}
	for _, file := range manifest.Files {
		if _, ok := listed[file.Path]; ok {
			return manifest, errors.New("duplicate manifest file")
		}
		listed[file.Path] = file
		path, e := regularPath(source, file.Path)
		if e != nil {
			return manifest, e
		}
		sum, size, e := hashFile(path)
		if e != nil {
			return manifest, e
		}
		if sum != file.Hash || size != file.Size {
			return manifest, errors.New("backup file hash/size mismatch")
		}
	}
	_, ok := listed["library.sqlite"]
	if !ok {
		return manifest, errors.New("backup database missing")
	}
	db, err := readonlyDB(filepath.Join(source, "library.sqlite"))
	if err != nil {
		return manifest, err
	}
	defer db.Close()
	schema, err := dbIntegrity(db)
	if err != nil {
		return manifest, err
	}
	if schema != manifest.Schema {
		return manifest, errors.New("backup schema mismatch")
	}
	refs, err := primaryRefs(db, schema)
	if err != nil {
		return manifest, err
	}
	if len(listed) != len(refs)+1 {
		return manifest, errors.New("backup contains unexpected or missing files")
	}
	for path, hash := range refs {
		file, ok := listed[path]
		if !ok || file.Hash != hash {
			return manifest, errors.New("backup primary reference missing")
		}
	}
	return manifest, nil
}
func Restore(ctx context.Context, source, destination string) error {
	return restore(ctx, source, destination, false)
}

// RepackFiles restores a verified backup into a new root with sharded paths.
// Source data stays untouched and incomplete output cannot be opened.
func RepackFiles(ctx context.Context, source, destination string) error {
	// Resolve parent aliases before reserving output: never create it inside
	// the input backup, even when the caller uses a symlink to its parent.
	sourcePath, err := filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	sourcePath, err = filepath.Abs(sourcePath)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return err
	}
	target, err := filepath.Abs(filepath.Join(parent, filepath.Base(destination)))
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(sourcePath, target)
	if err != nil {
		return err
	}
	if relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return errors.New("repack output must be outside the source backup")
	}
	return restore(ctx, sourcePath, target, true)
}

func restore(ctx context.Context, source, destination string, repack bool) error {
	manifest, err := VerifyBackup(source)
	if err != nil {
		return err
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return err
	}
	output, err := reserveTransfer(destination)
	if err != nil {
		return err
	}
	for _, file := range manifest.Files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err = transferFile(source, output, file.Path, file.Hash); err != nil {
			return err
		}
	}
	if repack {
		if err = repackCopiedFiles(ctx, output, manifest.Schema); err != nil {
			return err
		}
	}
	if err = recoverCopiedDeliveries(output, manifest.Schema); err != nil {
		return err
	}
	if err = syncedWrite(filepath.Join(output, restorePause), []byte("Workers and delivery paused. Resume explicitly with -resume-restored.\n")); err != nil {
		return err
	}
	if err = syncDirectory(output); err != nil {
		return err
	}
	if err = os.Remove(filepath.Join(output, transferMarker)); err != nil {
		return err
	}
	return syncDirectory(output)
}
func RestoredPaused(root string) bool {
	_, err := os.Lstat(filepath.Join(root, restorePause))
	return err == nil
}
func ResumeRestored(root string) error {
	err := os.Remove(filepath.Join(root, restorePause))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return syncDirectory(root)
}

// Activity after the snapshot cannot be reconstructed. Preserve message IDs
// already confirmed in it, but quarantine every unfinished imported delivery.
func recoverCopiedDeliveries(root string, schema int) error {
	if schema < 8 {
		return nil
	}
	u := url.URL{Scheme: "file", Path: filepath.Join(root, "library.sqlite")}
	q := u.Query()
	q.Set("mode", "rw")
	q.Add("_pragma", "synchronous(FULL)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE deliveries SET state='unknown',error='Відновлено з резервної копії: перевірте Telegram. Повтору немає.' WHERE state IN ('preview','queued','sending','failed','unknown')`); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE delivery_parts SET state='unknown',updated_at=? WHERE state<>'sent' AND delivery_id IN (SELECT id FROM deliveries WHERE state='unknown')`, nowUTC()); err != nil {
		return err
	}
	return tx.Commit()
}
