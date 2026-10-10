package fetch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rclone/rclone/fs"
)

var defaultOptions = []string{"ena-ftp", "sra-aws", "sra-tools"}

var fastqParity = regexp.MustCompile(`.*(1|2)\.f.*q(\.gz)?$`)

// Per-URL retry policy inside downloadFastqs. Three attempts (base
// delays 10 s / 30 s / 90 s) give a transient hiccup (TCP reset,
// slow-start timeout on a congested FTP server) a chance to clear
// without burning the whole method-level fallback budget. A
// permanent error (404, auth) breaks out immediately via
// errPermanent.
const (
	fetchMaxAttempts = 3
	fetchBackoff1    = 10 * time.Second
	fetchBackoff2    = 30 * time.Second
	fetchBackoff3    = 90 * time.Second
)

// errPermanent wraps a fetch error that should NOT be retried. The
// retry loop checks errors.Is(err, errPermanent) and bails out
// immediately so a 404 or auth failure doesn't waste the full retry
// budget. Transient wrapper for the kind of error — the underlying
// error is still joined for logging/display.
var errPermanent = errors.New("permanent fetch failure (no retry)")

// permanentStatusRe matches HTTP 4xx codes (except 408/429, which
// are retryable) and 410, as whole numbers in the error string. We
// use a regex rather than space-bordered substring matches because
// error strings may put the code at the end ("returned 403") or
// embedded in a longer phrase.
var permanentStatusRe = regexp.MustCompile(`\b(400|401|403|404|410)\b`)

// classifyFetchError returns errPermanent when the underlying error
// is non-retryable (HTTP 4xx that isn't 408/429, parse errors,
// malformed URL). Everything else (timeout, connection reset, 5xx,
// EOF mid-stream, MD5 mismatch after a complete-looking download)
// is treated as retryable — the next attempt may succeed.
func classifyFetchError(err error) error {
	if err == nil {
		return nil
	}
	s := strings.ToLower(err.Error())
	// Phrase-based permanent signals.
	permanentNeedles := []string{
		"not found", "forbidden", "unauthorized",
		"malformed", "invalid url",
	}
	for _, n := range permanentNeedles {
		if strings.Contains(s, n) {
			return fmt.Errorf("%w: %v", errPermanent, err)
		}
	}
	// Status-code permanent signals (word-bounded).
	if permanentStatusRe.MatchString(s) {
		return fmt.Errorf("%w: %v", errPermanent, err)
	}
	// Everything else — timeout, connection reset, EOF, 500/502/503/504,
	// MD5 mismatch, "temporary failure in name resolution" — stays
	// retryable so the next attempt gets a shot.
	return err
}

// retryFetchURL runs attempt() up to fetchMaxAttempts times, sleeping
// between retries, and short-circuits on a permanent error.
// errPermanent is unwrapped and the underlying error returned so the
// caller's log line reads naturally.
func retryFetchURL(method, url string, attempt func() error) error {
	backoffs := []time.Duration{fetchBackoff1, fetchBackoff2, fetchBackoff3}
	var lastErr error
	for i := 0; i < fetchMaxAttempts; i++ {
		if i > 0 {
			d := backoffs[i-1]
			log.Printf("↩️  %s retry %d/%d for %s in %s (previous: %v)", method, i+1, fetchMaxAttempts, url, d, lastErr)
			time.Sleep(d)
		}
		err := attempt()
		if err == nil {
			return nil
		}
		classified := classifyFetchError(err)
		if errors.Is(classified, errPermanent) {
			return err
		}
		lastErr = err
	}
	return fmt.Errorf("%s failed after %d attempts: %w", method, fetchMaxAttempts, lastErr)
}

// Per-worker circuit breaker. Each channel ("ena-ftp", "ena-aspera",
// "sra-aws", "sra-tools") carries a consecutive-failure counter and a
// demoted flag. After channelDemoteThreshold consecutive failures on
// this worker (process), the channel is reordered to the END of the
// options list for subsequent runs — further runs still try the
// others first. A single success resets both counter and demotion.
//
// Scoped per-worker DELIBERATELY: reliability of EBI vs NCBI depends
// on the worker's network path (region, firewall, DNS), so one
// unlucky worker's demotion must not propagate to healthy workers.
// A global server-side demotion would do exactly that; it's wrong.
const channelDemoteThreshold = 3

var channelBreaker struct {
	mu       sync.Mutex
	failures map[string]int
	demoted  map[string]bool
	warned   map[string]bool
}

func init() {
	channelBreaker.failures = make(map[string]int)
	channelBreaker.demoted = make(map[string]bool)
	channelBreaker.warned = make(map[string]bool)
}

func recordChannelFailure(channel string) {
	channelBreaker.mu.Lock()
	defer channelBreaker.mu.Unlock()
	channelBreaker.failures[channel]++
	if channelBreaker.failures[channel] >= channelDemoteThreshold && !channelBreaker.demoted[channel] {
		channelBreaker.demoted[channel] = true
		log.Printf("⚠️ fetch channel %q demoted after %d consecutive failures on this worker; subsequent fetches will try other channels first",
			channel, channelBreaker.failures[channel])
	}
}

func recordChannelSuccess(channel string) {
	channelBreaker.mu.Lock()
	defer channelBreaker.mu.Unlock()
	if channelBreaker.demoted[channel] {
		log.Printf("✅ fetch channel %q restored to normal priority after a successful fetch", channel)
	}
	channelBreaker.failures[channel] = 0
	channelBreaker.demoted[channel] = false
}

// reorderOptionsForBreaker moves every demoted channel to the END of
// the options list while preserving relative order inside each
// group. The caller's slice is NOT mutated.
func reorderOptionsForBreaker(options []string) []string {
	channelBreaker.mu.Lock()
	defer channelBreaker.mu.Unlock()
	if len(channelBreaker.demoted) == 0 {
		return options
	}
	healthy := make([]string, 0, len(options))
	demoted := make([]string, 0, len(options))
	for _, o := range options {
		if channelBreaker.demoted[o] {
			demoted = append(demoted, o)
		} else {
			healthy = append(healthy, o)
		}
	}
	return append(healthy, demoted...)
}

// warnAsperaOnce emits a one-shot deprecation warning the first time
// a caller explicitly opts into ena-aspera on this worker. ENA's
// aspera endpoint has rejected authentication for years in practice
// (verified 2026-10-09 against ERR527141 with both DSA and RSA
// bypass keys on ascli 4.27.5): every attempt wastes ~75 s on auth
// retries before falling through to the next method.
func warnAsperaOnce() {
	channelBreaker.mu.Lock()
	defer channelBreaker.mu.Unlock()
	if channelBreaker.warned["ena-aspera"] {
		return
	}
	channelBreaker.warned["ena-aspera"] = true
	log.Printf("⚠️ ena-aspera is unmaintained at ENA; auth consistently fails in practice. Use ena-ftp or sra-aws / sra-tools instead. Keeping the attempt for backward compat.")
}

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
		if option == "ena-aspera" {
			warnAsperaOnce()
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

	// Reorder options to push circuit-breaker-demoted channels to the
	// end of the list. New callers still try the healthy channels
	// first; a worker whose EBI path is sick (3 consecutive failures)
	// skips to NCBI immediately instead of burning the per-URL retry
	// budget on EBI first every time.
	options = reorderOptionsForBreaker(options)

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
				recordChannelSuccess(option)
				return nil
			} else {
				recordChannelFailure(option)
				log.Printf("FastqBackend failed on SRA sra-tools : %v", err)
				continue
			}
		}

		if option == "sra-aws" {
			err := fb.fetchFromSRA_AWS(runAccession, absPath, onlyRead1)
			sraToolTested = true
			if err == nil {
				recordChannelSuccess(option)
				return nil
			} else {
				recordChannelFailure(option)
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
			recordChannelSuccess(option)
			return nil
		}
		recordChannelFailure(option)
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

		// Each per-URL transfer runs under retryFetchURL: up to 3
		// attempts with 10 s / 30 s / 90 s backoff. The attempt closure
		// does the actual transfer plus the MD5 verification — MD5
		// mismatch is treated as a retryable transfer failure (the
		// bytes made it to disk but the content is wrong, usually
		// because the TCP connection reset mid-stream).
		var transferErr error
		switch method {
		case "fastq_ftp":
			transferErr = retryFetchURL(method, url, func() error {
				ftpURI, err := ParseURI("ftp://" + url)
				if err != nil {
					// Malformed URL — permanent, don't retry.
					return fmt.Errorf("invalid url %s: %w", url, err)
				}
				ftpFs, err := ftpURI.fs()
				if err != nil {
					return fmt.Errorf("could not open ftp transmission on %s: %w", url, err)
				}

				if dst.File == "" {
					dst.File = ftpURI.File
				}

				if err := ftpFs.fs.Copy(dstFs, *ftpURI, dst, true); err != nil {
					return fmt.Errorf("ftp transmission failed for %s: %w", url, err)
				}
				return verifyFastqMD5(dstFs, dst, md5)
			})
		case "fastq_aspera":
			transferErr = retryFetchURL(method, url, func() error {
				faspURL := "fasp://era-fasp@" + url
				faspURI, err := ParseURI(faspURL)
				if err != nil {
					return fmt.Errorf("invalid url %s: %w", faspURL, err)
				}
				faspFs, err := faspURI.fs()
				if err != nil {
					return fmt.Errorf("could not open Aspera transmission on %s: %w", faspURL, err)
				}

				if dst.File == "" {
					dst.File = faspURI.File
				}

				if err := faspFs.fs.Copy(dstFs, *faspURI, dst, true); err != nil {
					return fmt.Errorf("aspera transmission failed for %s: %w", faspURL, err)
				}
				log.Printf("**** Aspera copy %s -> %s ****", *faspURI, dst)
				return verifyFastqMD5(dstFs, dst, md5)
			})
		default:
			continue
		}

		if transferErr != nil {
			log.Printf("fetch %s %s: %v", method, url, transferErr)
			return false
		}
	}
	return true
}

// verifyFastqMD5 checks the downloaded file's MD5 against the one
// ENA's filereport API gave us. Semantics:
//   - expectedMD5 == "" (study-dependent: ENA sometimes returns no
//     md5): emit a visible warning and accept, matching the previous
//     "hoping for the best" posture.
//   - expectedMD5 != "" and Info/getMD5 can't produce one: HARD FAIL
//     — before this change the retry path silently accepted a
//     possibly-truncated file in these branches; that's exactly how
//     "incomplete samples" slip through.
//   - expectedMD5 mismatches the file's MD5: hard fail (and retryable
//     from retryFetchURL's perspective — a mid-stream reset can
//     produce complete-looking garbage).
func verifyFastqMD5(dstFs FileSystemInterface, dst URI, expectedMD5 string) error {
	if expectedMD5 == "" {
		log.Printf("⚠️ fastq: no MD5 provided by ENA for %s — accepting without integrity check", dst)
		return nil
	}
	obj, err := dstFs.Info(dst.CompletePath())
	if err != nil {
		return fmt.Errorf("md5 verification: backend Info failed for %s (expected md5=%s): %w", dst, expectedMD5, err)
	}
	o, ok := obj.(fs.Object)
	if !ok {
		return fmt.Errorf("md5 verification: destination %s is not a file (expected md5=%s)", dst, expectedMD5)
	}
	objectMd5, err := getMD5(o)
	if err != nil {
		return fmt.Errorf("md5 verification: getMD5 failed for %s (expected md5=%s): %w", dst, expectedMD5, err)
	}
	if objectMd5 != expectedMD5 {
		return fmt.Errorf("md5 mismatch for %s: expected %s, got %s", dst, expectedMD5, objectMd5)
	}
	return nil
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
