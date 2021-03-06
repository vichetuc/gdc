package gdc

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/dropbox/dropbox-sdk-go-unofficial/dropbox/files"
)

// SortableMetadata puts sort.Interface on files.IsMetadata
type SortableMetadata []files.IsMetadata

func (slice SortableMetadata) Len() int {
	return len(slice)
}

func (slice SortableMetadata) Less(i, j int) bool {
	m1 := slice[i]
	m2 := slice[j]
	switch m1t := m1.(type) {
	case *files.FolderMetadata:
		switch m2t := m2.(type) {
		case *files.FolderMetadata:
			return strings.Compare(m1t.Name, m2t.Name) < 0
		}
		return true
	case *files.FileMetadata:
		switch m2t := m2.(type) {
		case *files.FileMetadata:
			return strings.Compare(m1t.Name, m2t.Name) < 0
		}
		return false
	}
	return false
}

func (slice SortableMetadata) Swap(i, j int) {
	slice[i], slice[j] = slice[j], slice[i]
}

// Lister provides access to file listings
type Lister struct {
	Options
	mu    sync.Mutex
	paths map[string]SortableMetadata
	wg    sync.WaitGroup
	dbx   files.Client
}

// NewLister creates a new Lister instance
func NewLister(options *Options) *Lister {
	return &Lister{
		Options: *options,
		paths:   make(map[string]SortableMetadata),
		dbx:     files.New(options.Config),
	}
}

// List files and folders inside the given remote path. This can be recursive depending on the provided Options.
func (l *Lister) List() {
	paths := l.Paths
	if len(paths) == 0 {
		paths = []string{""}
	}
	for _, path := range paths {
		if l.Verbose {
			fmt.Println("Listing files in", path, "(recursively: ", l.Recursive, ")")
		}
		l.GetListing(path)
		l.print()
	}
}

// GetMetadata fetches metadata for a path
func (l *Lister) GetMetadata(path string) files.IsMetadata {
	md, err := l.dbx.GetMetadata(files.NewGetMetadataArg(FixPath(path)))
	if err != nil {
		panic(err)
	}
	return md
}

// GetListing fetches the listing from Dropbox
func (l *Lister) GetListing(path string) map[string]SortableMetadata {
	path = FixPath(path)
	a := files.NewListFolderArg(path)
	a.Recursive = l.Recursive
	r, err := l.dbx.ListFolder(a)
	if err != nil {
		panic(err)
	}
	for len(r.Entries) > 0 {
		l.wg.Add(1)
		go l.processServerResponse(path, r.Entries)
		if !r.HasMore {
			break
		}
		r, err = l.dbx.ListFolderContinue(files.NewListFolderContinueArg(r.Cursor))
		if err != nil {
			panic(err)
		}
	}
	l.wg.Wait()
	return l.paths
}

func (l *Lister) processServerResponse(path string, entries []files.IsMetadata) {
	for _, fi := range entries {
		var m *files.Metadata
		switch md := fi.(type) {
		case *files.FileMetadata:
			m = &md.Metadata
		case *files.FolderMetadata:
			m = &md.Metadata

			// Also put the folder itself into the map when listing recursive.
			// In case there are no files in there it would not be listed otherwise
			if l.Recursive {
				l.mu.Lock()
				l.paths[m.PathDisplay] = append(l.paths[m.PathDisplay], nil)
				l.mu.Unlock()
			}
		}
		if path == m.PathDisplay {
			continue
		}
		filePath := l.extractPath(m)
		l.mu.Lock()
		l.paths[filePath] = append(l.paths[filePath], fi)
		l.mu.Unlock()
	}
	l.wg.Done()
}

func (l *Lister) extractPath(md *files.Metadata) string {
	p := path.Dir(md.PathDisplay)
	if p == "." {
		return "/"
	}
	return p
}

func (l *Lister) print() {
	filePaths := make([]string, 0)
	for filePath := range l.paths {
		filePaths = append(filePaths, filePath)
	}
	sort.Strings(filePaths)
	for _, filePath := range filePaths {
		mds := l.paths[filePath]
		sort.Sort(mds)
		if l.Recursive {
			fmt.Println(filePath + ":")
		}
		totalBytes := uint64(0)
		for _, md := range mds {
			switch m := md.(type) {
			case *files.FileMetadata:
				totalBytes += m.Size
			}
		}
		fmt.Println("total", l.convertSize(totalBytes))
		for _, md := range mds {
			switch m := md.(type) {
			case *files.FolderMetadata:
				fmt.Println("[d]\t" + m.PathDisplay)
			case *files.FileMetadata:
				fmt.Println("[f]\t" + l.convertSize(m.Size) + "\t" + m.ServerModified.String() + "\t" + m.PathDisplay)
			}
		}
	}
}

func (l *Lister) convertSize(size uint64) string {
	if l.HumanReadable {
		return HumanReadableBytes(size)
	}
	return strconv.FormatUint(size, 10)
}
