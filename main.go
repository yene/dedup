package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/karrick/godirwalk"
)

const (
	B   uint64 = 1
	KiB        = B << 10
	MiB        = KiB << 10
	GiB        = MiB << 10
	TiB        = GiB << 10
	KB         = B * 1000
	MB         = KB * 1000
	GB         = MB * 1000
	TB         = GB * 1000
)

type File struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	SizeHuman string `json:"sizehuman"`
	Duplicate bool   `json:"-"`
	CRC32Hash string `json:"crc32"`
}

type Stats struct {
	Start          time.Time
	Elapsed        time.Duration
	SeenFiles      uint64 // count of all seen
	FilteredFiles  uint64 // count of ones that were filtered out by filesize
	DuplicateFiles uint64
	WastedSpace    int64
}

func main() {
	dirnamePtr := flag.String("d", "", "starting directory")
	jsonResultPtr := flag.Bool("json", false, "output result as json")
	minFileSizePtr := flag.Int("minsize", int(30*MiB), "minimum filesize in bytes, default 30MiB")
	flag.Parse()

	filter := []string{".git", ".terraform", "node_modules"}
	stat := Stats{}
	files := make([]File, 0, 1024)
	dirname := *dirnamePtr
	if dirname == "" {
		flag.PrintDefaults()
		os.Exit(1)
	}

	dirname = expandTilde(dirname)
	minFileSize := *minFileSizePtr
	stat.Start = time.Now()
	err := godirwalk.Walk(dirname, &godirwalk.Options{
		Callback: func(osPathname string, de *godirwalk.Dirent) error {
			for _, f := range filter {
				if strings.HasSuffix(osPathname, "/"+f) {
					return godirwalk.SkipThis
				}
			}
			if de.IsDir() {
				return nil
			}
			s, err := os.Stat(osPathname)
			if err != nil {
				return nil
			}
			size := s.Size()
			stat.SeenFiles = stat.SeenFiles + 1
			if size > int64(minFileSize) {
				f := File{Path: osPathname, Size: size, SizeHuman: ByteCountSI(size), CRC32Hash: ""}
				// log.Printf("%s %s\n", de.ModeType(), osPathname)
				files = append(files, f)
			}
			return nil
		},
		Unsorted: true,
	})

	if err != nil {
		panic(err)
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Size > files[j].Size
	})

	l := len(files)
	stat.FilteredFiles = uint64(l)

	duplicateFiles := make([]File, 0, l)
	for i := 0; i < l; i++ {
		checkFile := files[i]
		if checkFile.Duplicate {
			continue
		}
		hasDuplicate := false
		for j := i + 1; j < l; j++ {
			f := files[j]
			if f.Duplicate {
				continue
			}
			if f.Size == checkFile.Size {
				f.Duplicate = true
				hasDuplicate = true
				duplicateFiles = append(duplicateFiles, f)
				files[j] = f
			}
		}
		if hasDuplicate {
			checkFile.Duplicate = true
			duplicateFiles = append(duplicateFiles, checkFile)
		}
	}

	hm := make(map[string][]File)
	for index, _ := range duplicateFiles {
		f := duplicateFiles[index]
		hash, err := CRC32Hash(f.Path)
		if err != nil {
			log.Println(err)
			continue
		}
		f.CRC32Hash = hash
		val, exists := hm[hash]
		if !exists {
			val = []File{}
		}
		val = append(val, f)
		hm[hash] = val
	}
	for key, slice := range hm {
		// Prune files that were added because of same size but don't have CRC32 duplicate.
		if len(slice) == 1 {
			delete(hm, key)
			continue
		}
		stat.DuplicateFiles = stat.DuplicateFiles + uint64(len(slice))
		size := slice[0].Size
		stat.WastedSpace = stat.WastedSpace + (size * int64(len(slice)-1))
	}

	stat.Elapsed = time.Since(stat.Start)

	log.Printf("Dedup took: %s", stat.Elapsed)
	log.Println("Seen files count:", stat.SeenFiles)
	log.Println("Checked files count:", stat.FilteredFiles)
	log.Println("Duplicate files count:", stat.DuplicateFiles)
	log.Println("Wasted space:", ByteCountSI(stat.WastedSpace))

	if *jsonResultPtr {
		jb, _ := json.MarshalIndent(hm, "", "  ")
		fmt.Println(string(jb))
	}
}

func CRC32Hash(filePath string) (string, error) {
	var returnCRC32String string
	var polynomial uint32 = 0xedb88320
	file, err := os.Open(filePath)
	if err != nil {
		return returnCRC32String, err
	}
	defer file.Close()
	tablePolynomial := crc32.MakeTable(polynomial)
	hash := crc32.New(tablePolynomial)
	if _, err := io.Copy(hash, file); err != nil {
		return returnCRC32String, err
	}
	hashInBytes := hash.Sum(nil)[:]
	returnCRC32String = hex.EncodeToString(hashInBytes)
	return returnCRC32String, nil
}

func expandTilde(path string) string {
	if strings.HasPrefix(path, "~/") {
		usr, err := user.Current()
		if err == nil {
			return filepath.Join(usr.HomeDir, path[2:])
		}
	}
	return path
}

func ByteCountSI(b int64) string {
	const unit = 1000
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "kMGTPE"[exp])
}

func ByteCountIEC(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
