package fs

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"syscall"
)

type realFS struct {
	// Stores the file entries for directories we've listed before
	entries map[string]entriesOrErr

	// For the current working directory
	cwd string
}

type entriesOrErr struct {
	entries map[string]*Entry
	err     error
}

func RealFS() FS {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	} else {
		// Resolve symlinks in the current working directory. Symlinks are resolved
		// when input file paths are converted to absolute paths because we need to
		// recognize an input file as unique even if it has multiple symlinks
		// pointing to it. The build will generate relative paths from the current
		// working directory to the absolute input file paths for error messages,
		// so the current working directory should be processed the same way. Not
		// doing this causes test failures with esbuild when run from inside a
		// symlinked directory.
		//
		// This deliberately ignores errors due to e.g. infinite loops. If there is
		// an error, we will just use the original working directory and likely
		// encounter an error later anyway. And if we don't encounter an error
		// later, then the current working directory didn't even matter and the
		// error is unimportant.
		if path, err := filepath.EvalSymlinks(cwd); err == nil {
			cwd = path
		}
	}
	return &realFS{
		entries: make(map[string]entriesOrErr),
		cwd:     cwd,
	}
}

func (fs *realFS) ReadDirectory(dir string) (map[string]*Entry, error) {
	// First, check the cache
	cached, ok := fs.entries[dir]

	// Cache hit: stop now
	if ok {
		return cached.entries, cached.err
	}

	// Cache miss: read the directory entries
	names, err := readdir(dir)
	entries := make(map[string]*Entry)
	if err == nil {
		for _, name := range names {
			// Call "stat" lazily for performance. The "@material-ui/icons" package
			// contains a directory with over 11,000 entries in it and running "stat"
			// for each entry was a big performance issue for that package.
			entries[name] = &Entry{
				dir:      dir,
				base:     name,
				needStat: true,
			}
		}
	}

	// Update the cache unconditionally. Even if the read failed, we don't want to
	// retry again later. The directory is inaccessible so trying again is wasted.
	if err != nil {
		entries = nil
	}
	fs.entries[dir] = entriesOrErr{entries: entries, err: err}
	return entries, err
}

func (fs *realFS) ReadFile(path string) (string, error) {
	BeforeFileOpen()
	defer AfterFileClose()
	buffer, err := ioutil.ReadFile(path)

	// Unwrap to get the underlying error
	if pathErr, ok := err.(*os.PathError); ok {
		err = pathErr.Unwrap()
	}

	// Windows returns ENOTDIR here even though nothing we've done yet has asked
	// for a directory. This really means ENOENT on Windows. Return ENOENT here
	// so callers that check for ENOENT will successfully detect this file as
	// missing.
	if err == syscall.ENOTDIR {
		return "", syscall.ENOENT
	}

	return string(buffer), err
}

func (fs *realFS) ModKey(path string) (ModKey, error) {
	BeforeFileOpen()
	defer AfterFileClose()
	return modKey(path)
}

func (*realFS) IsAbs(p string) bool {
	return filepath.IsAbs(p)
}

func (*realFS) Abs(p string) (string, bool) {
	abs, err := filepath.Abs(p)
	return abs, err == nil
}

func (*realFS) Dir(p string) string {
	return filepath.Dir(p)
}

func (*realFS) Base(p string) string {
	return filepath.Base(p)
}

func (*realFS) Ext(p string) string {
	return filepath.Ext(p)
}

func (*realFS) Join(parts ...string) string {
	return filepath.Clean(filepath.Join(parts...))
}

func (fs *realFS) Cwd() string {
	return fs.cwd
}

func (*realFS) Rel(base string, target string) (string, bool) {
	if rel, err := filepath.Rel(base, target); err == nil {
		return rel, true
	}
	return "", false
}

func readdir(dirname string) ([]string, error) {
	BeforeFileOpen()
	defer AfterFileClose()
	f, err := os.Open(dirname)

	// Unwrap to get the underlying error
	if pathErr, ok := err.(*os.PathError); ok {
		err = pathErr.Unwrap()
	}

	// Windows returns ENOTDIR here even though nothing we've done yet has asked
	// for a directory. This really means ENOENT on Windows. Return ENOENT here
	// so callers that check for ENOENT will successfully detect this directory
	// as missing.
	if err == syscall.ENOTDIR {
		return nil, syscall.ENOENT
	}

	// Stop now if there was an error
	if err != nil {
		return nil, err
	}

	defer f.Close()
	entries, err := f.Readdirnames(-1)

	// Unwrap to get the underlying error
	if syscallErr, ok := err.(*os.SyscallError); ok {
		err = syscallErr.Unwrap()
	}

	// Don't convert ENOTDIR to ENOENT here. ENOTDIR is a legitimate error
	// condition for Readdirnames() on non-Windows platforms.

	return entries, err
}