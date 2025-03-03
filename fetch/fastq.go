package fetch

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"

	"github.com/rclone/rclone/fs"
)

var defaultOptions = []string{"ena-aspera", "ena-ftp", "sra-tools"}

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
	if len(src.Options) > 0 {
		srcOptions = src.Options
	} else {
		srcOptions = defaultOptions
	}
	for _, option := range srcOptions {
		if !isLocal && (option == "ena-aspera" || option == "sra-tools") {
			log.Printf("Refection option %s as dst is not local\n", option)
			continue
		}
		options = append(options, option)
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
			err := fb.fetchFromSRA(runAccession, absPath)
			sraToolTested = true
			if err == nil {
				return nil
			} else {
				log.Printf("FastqBackend failed on SRA : %v", err)
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
					return fb.fetchFromSRA(runAccession, absPath)
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
				return fb.fetchFromSRA(runAccession, absPath)
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
		success := fb.downloadFastqs(method, urlList, md5s, dst, otherFs)
		if success {
			return nil
		}

		log.Printf("Download failed with method: %s, trying next method...", method)
	}

	return fmt.Errorf("failed to fetch %s with any method", src.String())
}

// downloadFastqs handles downloading FASTQ files using Aspera, FTP, or SRA.
func (fb *FastqBackend) downloadFastqs(method string, urls, md5s []string, folderDst URI, dstFs FileSystemInterface) bool {
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

// fetchFromSRA downloads a FASTQ file using SRA toolkit inside Docker.
func (fb *FastqBackend) fetchFromSRA(runAccession, destination string) error {
	//log.Printf("Fetching FASTQ from SRA: %s", runAccession)

	cmd := exec.Command("docker", "run", "--rm",
		"-v", destination+":/destination",
		"ncbi/sra-tools",
		"sh", "-c",
		fmt.Sprintf("cd /destination && prefetch %s && fasterq-dump -f --split-files %s && (for f in *.fastq; do gzip \"$f\" & done; wait; rm -fr %s) || exit 1", runAccession, runAccession, runAccession),
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
