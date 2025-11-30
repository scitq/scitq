package fetch

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"path"
	"regexp"
	"strings"

	"github.com/rclone/rclone/fs"
)

var defaultOptions = []string{"ena-ftp", "sra-aws", "sra-tools"}

var fastqParity = regexp.MustCompile(`.*(1|2)\.f.*q(\.gz)?$`)

// FastqBackend handles downloading FASTQ files using FTP, Aspera, or SRA.
type FastqBackend struct{}

// NewFastqBackend initializes a FastqBackend.
func NewFastqBackend() *FastqBackend {
	return &FastqBackend{}
}

// Copy implements the Copy method for FastqBackend.
func (fb *FastqBackend) Copy(otherFs FileSystemInterface, src, dst URI, selfIsSource bool) error {
	if !selfIsSource {
		return fmt.Errorf("FastqBackend can only be used as a source")
	}

	var absPath string
	var err error
	local, isLocal := otherFs.(*LocalBackend)
	if !isLocal {
		log.Printf("FastqBackend to non local restrict options possibilities")
	} else {
		absPath, err = local.AbsolutePath(dst.Path)
		if err != nil {
			return fmt.Errorf("FastqBackend local destination is broken: %w", err)
		}
	}

	// finding appropriate options
	var options []string
	var srcOptions []string
	onlyRead1 := false

	for _, option := range src.Options {
		if option == "only-read1" { // modifier, not a transfer method
			onlyRead1 = true
			continue
		}
		if !isLocal && (option == "ena-aspera" || option == "sra-tools" || option == "sra-aws") {
			log.Printf("Rejecting option %s as dst is not local\n", option)
			continue
		}
		srcOptions = append(srcOptions, option)
	}

	if len(srcOptions) > 0 {
		options = srcOptions
	} else {
		options = defaultOptions
	}

	if len(options) == 0 {
		return fmt.Errorf("no more options remain for FastqBackend, try using less restrictive conditions")
	}

	// preparing items for option loop
	runAccession := src.Component
	sraToolTested := false
	enaMeta := false
	var run map[string]string
	var md5s []string

	// testing the different options in right order
	for _, option := range options {
		if option == "sra-tools" {
			err := fb.fetchFromSRA_sratool(runAccession, absPath, onlyRead1)
			sraToolTested = true
			if err == nil {
				return nil
			} else {
				log.Printf("FastqBackend failed on SRA sra-tools : %v", err)
				continue
			}
		}

		if option == "sra-aws" {
			err := fb.fetchFromSRA_AWS(runAccession, absPath, onlyRead1)
			sraToolTested = true
			if err == nil {
				return nil
			} else {
				log.Printf("FastqBackend failed on SRA AWS : %v", err)
				continue
			}
		}

		if !enaMeta {
			// Fetch metadata from ENA API
			ebiURL := fmt.Sprintf(
				"https://www.ebi.ac.uk/ena/portal/api/filereport?accession=%s&result=read_run&fields=fastq_md5,fastq_aspera,fastq_ftp,sra_md5,sra_ftp&format=json&download=true&limit=0",
				runAccession,
			)

			apiResponse, err := http.Get(ebiURL)
			if err != nil {
				return fmt.Errorf("failed to query ENA API: %v", err)
			}
			defer apiResponse.Body.Close()

			if apiResponse.StatusCode == 204 {
				log.Println("ENA API returned no data, falling back to SRA")
				if !sraToolTested && stringInSlice("sra-tools", options) {
					return fb.fetchFromSRA(runAccession, absPath, onlyRead1)
				} else {
					if sraToolTested {
						return fmt.Errorf("FastqBackend failed as ENA and SRA metadata retrieval failed")
					} else {
						return fmt.Errorf("FastqBackend failed as ENA failed and SRA is not possible")
					}
				}
			}

			body, err := io.ReadAll(apiResponse.Body)
			if err != nil {
				return fmt.Errorf("failed to read API response: %v", err)
			}

			var runs []map[string]string
			err = json.Unmarshal(body, &runs)
			if err != nil || len(runs) == 0 {
				log.Println("ENA API returned no valid data, falling back to SRA")
				return fb.fetchFromSRA(runAccession, absPath, onlyRead1)
			}

			run = runs[0]
			md5s = strings.Split(run["fastq_md5"], ";")
			enaMeta = true
		}

		var method string
		switch option {
		case "ena-aspera":
			method = "fastq_aspera"
		case "ena-ftp":
			method = "fastq_ftp"
		default:
			return fmt.Errorf("FastqBackend : unsupported option with ENA %s", option)
		}

		urls, found := run[method]
		if !found || urls == "" {
			continue
		}

		urlList := strings.Split(urls, ";")
		success := fb.downloadFastqs(method, urlList, md5s, dst, otherFs, onlyRead1)
		if success {
			return nil
		}

		log.Printf("Download failed with method: %s, trying next method...", method)
	}

	return fmt.Errorf("failed to fetch %s with any method", src.String())
}

// downloadFastqs handles downloading FASTQ files using Aspera, FTP, or SRA.
func (fb *FastqBackend) downloadFastqs(method string, urls, md5s []string, folderDst URI, dstFs FileSystemInterface, onlyRead1 bool) bool {
	// If onlyRead1 is requested, filter out R2 entries while preserving md5 alignment
	if onlyRead1 {
		filteredURLs := make([]string, 0, len(urls))
		filteredMD5s := make([]string, 0, len(md5s))
		for i, u := range urls {
			base := path.Base(u)
			m := fastqParity.FindStringSubmatch(base)
			if len(m) == 0 {
				// If we cannot determine parity, keep the file to be safe
				filteredURLs = append(filteredURLs, u)
				if i < len(md5s) {
					filteredMD5s = append(filteredMD5s, md5s[i])
				}
				continue
			}
			if m[1] == "1" {
				filteredURLs = append(filteredURLs, u)
				if i < len(md5s) {
					filteredMD5s = append(filteredMD5s, md5s[i])
				}
			}
		}
		urls = filteredURLs
		md5s = filteredMD5s
	}

	for i, url := range urls {
		dst := folderDst
		md5 := md5s[i]

		switch method {
		case "fastq_ftp":
			{
				// err = fetchFTP(url, destFile)
				ftpURI, err := ParseURI("ftp://" + url)
				if err != nil {
					log.Printf("ftp URL seems broken %s: %v", url, err)
					return false
				}
				ftpFs, err := ftpURI.fs()
				if err != nil {
					log.Printf("Could not open ftp transmission on %s: %v", url, err)
					return false
				}

				if dst.File == "" {
					dst.File = ftpURI.File
				}

				err = ftpFs.fs.Copy(dstFs, *ftpURI, dst, true)
				if err != nil {
					log.Printf("ftp transmission failed for %s: %v", url, err)
					return false
				}
			}
		case "fastq_aspera":
			{
				// err = fetchAspera(url, destFile)
				url = "fasp://era-fasp@" + url
				faspURI, err := ParseURI(url)
				if err != nil {
					log.Printf("Aspera URL seems broken %s: %v", url, err)
					return false
				}
				faspFs, err := faspURI.fs()
				if err != nil {
					log.Printf("Could not open Aspera transmission on %s: %v", url, err)
					return false
				}

				if dst.File == "" {
					dst.File = faspURI.File
				}

				err = faspFs.fs.Copy(dstFs, *faspURI, dst, true)
				log.Printf("**** Aspera copy %s -> %s ****", *faspURI, dst)
				if err != nil {
					log.Printf("Aspera transmission failed for %s: %v", url, err)
					return false
				}
			}

		default:
			continue
		}

		// Check MD5 hash
		obj, err := dstFs.Info(dst.CompletePath())
		if err != nil {
			log.Printf("Backend is not MD5 capable, could not check MD5 for file %s (for %s), hoping for the best: %v", dst, dst.CompletePath(), err)
		} else {
			o, ok := obj.(fs.Object)
			if !ok {
				log.Printf("ERROR : Destination file %s seems to be a folder", dst)
				return false
			}
			objectMd5, err := getMD5(o)
			if err != nil {
				log.Printf("ERROR : Could not obtain MD5 for file %s: %v", dst, err)
			}
			if objectMd5 != md5 {
				log.Printf("MD5 mismatch for %s", dst)
				return false
			}

		}
	}
	return true
}

func (fb *FastqBackend) fetchFromSRA(runAccession, destination string, onlyRead1 bool) error {
	// First try AWS srapath method
	err := fb.fetchFromSRA_AWS(runAccession, destination, onlyRead1)
	if err == nil {
		return nil
	}
	log.Printf("SRA fetch via AWS srapath failed: %v, falling back to standard sra-tools method", err)

	// Fallback to standard sra-tools method
	return fb.fetchFromSRA_sratool(runAccession, destination, onlyRead1)
}

// fetchFromSRA downloads a FASTQ file using SRA toolkit inside Docker.
func (fb *FastqBackend) fetchFromSRA_sratool(runAccession, destination string, onlyRead1 bool) error {
	//log.Printf("Fetching FASTQ from SRA: %s", runAccession)

	// Build an optional step to drop R2 fastqs before compression if requested
	dropR2 := ""
	if onlyRead1 {
		dropR2 = "for f in *2.fastq; do [ -e \"$f\" ] && rm \"$f\" ; done; "
	}

	cmd := exec.Command("docker", "run", "--rm",
		"-v", destination+":/destination",
		"ncbi/sra-tools",
		"sh", "-c",
		fmt.Sprintf(
			"cd /destination && prefetch -X 9999999999999 %s && fasterq-dump -f --split-files %s && (%sfor f in *.fastq; do gzip -1 \"$f\" & done; wait; rm -fr %s) || exit 1",
			runAccession, runAccession, dropR2, runAccession,
		),
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("SRA download error: %s", string(output))
		return fmt.Errorf("SRA fetch failed: %v", err)
	}

	//log.Println("SRA download successful")
	return nil
}

// List, Mkdir and Info are not supported for FastqBackend
func (fb *FastqBackend) List(path string) (fs.DirEntries, error) {
	return nil, fmt.Errorf("FastqBackend does not support list")
}
func (fb *FastqBackend) Mkdir(path string) error {
	return fmt.Errorf("FastqBackend does not support mkdir")
}
func (fb *FastqBackend) Info(path string) (fs.DirEntry, error) {
	return nil, fmt.Errorf("FastqBackend does not support list")
}

// fetchFromSRA downloads a FASTQ file using SRA toolkit with AWS srapath inside Docker.
func (fb *FastqBackend) fetchFromSRA_AWS(runAccession, destination string, onlyRead1 bool) error {
	dropR2 := ""
	if onlyRead1 {
		dropR2 = `for f in *2.fastq; do [ -e "$f" ] && rm "$f"; done;`
	}

	script := fmt.Sprintf(`
		set -e
		RUNACCESSION=%s

		cd /destination
		SRAPATH=$(srapath $RUNACCESSION | grep 's3.amazonaws.com' | head -n 1)
		if [ -z "$SRAPATH" ]; then exit 1; fi

		wget -q "$SRAPATH" -O "$RUNACCESSION"
		fasterq-dump -f --split-files "./$RUNACCESSION"
		%s

		for f in *.fastq; do gzip -1 "$f" & done
		wait

		rm -f "$RUNACCESSION"
	`, runAccession, dropR2)

	cmd := exec.Command(
		"docker", "run", "--rm",
		"-v", destination+":/destination",
		"ncbi/sra-tools",
		"sh", "-c", script,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("SRA fetch failed: %v (%s)", err, string(output))
	}

	return nil
}
