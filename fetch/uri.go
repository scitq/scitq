package fetch

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
)

var localPathRegex = regexp.MustCompile(`^(?P<component>[a-zA-Z]:\\|/|\.[/\\]|\.\.[/\\])?((?P<path>(?:[\w\s.-]+[/\\]?)+)(?P<separator>[/\\]))?(?P<file>[^/\\]*)$`)
var uriRegex = regexp.MustCompile(`^(?P<proto>[a-z0-9+]*)(?P<options>(@[a-z0-9_-]+)*)?:\/\/((?P<user>[^@:]+)(:(?P<password>[^@]+))?@)?(?P<component>[^/]+)(\/((?P<path>[^|]*)/)?(?P<file>[^|]*))?(?P<actions>(\|[^\|]+)*)$`)

type URI struct {
	Proto     string
	Options   []string
	User      string
	Password  string
	Component string
	Path      string
	File      string
	Separator string
	Actions   []string
}

// ParseURI parses a URI using a regular expression and returns the appropriate backend and path.
func ParseURI(uri_string string) (*URI, error) {
	matches := uriRegex.FindStringSubmatch(uri_string)
	if matches == nil {
		// most cases of local files will be handled here except file://./path/to/my/file
		matches = localPathRegex.FindStringSubmatch(uri_string)
		if matches == nil {
			if strings.HasPrefix(uri_string, "file://") {
				return ParseURI(uri_string[7:])
			} else {
				return nil, fmt.Errorf("failed to parse URI %s", uri_string)
			}
		} else {
			component := matches[localPathRegex.SubexpIndex("component")]
			separator := "/"
			if s := matches[localPathRegex.SubexpIndex("separator")]; s != "" {
				separator = s
			}
			if l := len(component); l > 0 {
				if separator == "" {
					separator = string(component[l-1])
				}
				component = component[:l-1]
			}
			if component == "" {
				component = "."
			}
			return &URI{
				Proto:     "file",
				Component: component,
				Separator: separator,
				Path:      matches[localPathRegex.SubexpIndex("path")],
				File:      matches[localPathRegex.SubexpIndex("file")],
			}, nil
		}

	}

	var uri URI

	// Extract components from the URI
	uri.Proto = matches[uriRegex.SubexpIndex("proto")]
	uri.Separator = "/"
	if options := matches[uriRegex.SubexpIndex("options")]; options != "" {
		uri.Options = strings.Split(options[1:], "@")
	}
	uri.User = matches[uriRegex.SubexpIndex("user")]
	uri.Password = matches[uriRegex.SubexpIndex("password")]
	if uri.Proto == "file" {
		// cover the case of file://./path/to/myfile
		uri.Component = matches[uriRegex.SubexpIndex("component")] + "/"
	} else {
		uri.Component = matches[uriRegex.SubexpIndex("component")]
	}
	uri.Path = matches[uriRegex.SubexpIndex("path")]
	uri.File = matches[uriRegex.SubexpIndex("file")]
	if actions := matches[uriRegex.SubexpIndex("actions")]; actions != "" {
		uri.Actions = strings.Split(actions[1:], "|")
	}

	return &uri, nil
}

func (uri URI) fs() (*MetaFileSystem, error) {
	// Default to rclone if the protocol is unknown or not explicitly handled
	switch uri.Proto {
	case "file":
		ctx := context.Background()
		return NewMetaFileSystem(NewLocalBackend(ctx, uri.Component+uri.Separator))
	case "ftp", "ftps":
		ftpOptions := map[string]string{
			"host":         uri.Component,
			"user":         uri.User,
			"pass":         uri.Password,
			"explicit_tls": strconv.FormatBool(uri.Proto == "ftps"),
		}
		// **🚀 Detect anonymous FTP and set correct login**
		if ftpOptions["user"] == "" {
			ftpOptions["user"] = "anonymous"
			ftpOptions["pass"] = "anonymous@domain.com" // Some servers require a dummy email
		}
		ftpRemote, err := addRemoteInMemory("ftp", ftpOptions)
		if err != nil {
			log.Printf("Error adding FTP remote on %s: %v", uri.Component, err)
			return nil, err
		}
		url := ftpRemote + ":"
		ctx := context.Background()
		return NewMetaFileSystem(NewRcloneBackend(ctx, url))
	case "http", "https":
		url := uri.Proto + "://" + uri.Component
		httpOptions := map[string]string{
			"url":      url,
			"user":     uri.User,
			"password": uri.Password,
		}
		httpRemote, err := addRemoteInMemory("http", httpOptions)
		if err != nil {
			log.Printf("Error adding HTTP remote on %s: %v", url, err)
			return nil, err
		}
		new_url := httpRemote + ":"
		ctx := context.Background()
		return NewMetaFileSystem(NewRcloneBackend(ctx, new_url))
	case "fasp":
		return NewMetaFileSystem(&AsperaBackend{}, nil)
	case "fastq", "run+fastq":
		return NewMetaFileSystem(&FastqBackend{}, nil)
	default:
		// Check if the protocol is present in rclone configuration
		if stringInSlice(uri.Proto, RcloneRemotes) {
			rclone_uri := uri.Proto + ":" + uri.Component
			ctx := context.Background()
			return NewMetaFileSystem(NewRcloneBackend(ctx, rclone_uri))
		}
		return nil, fmt.Errorf("unsupported URI protocol: %s (protocols %v)", uri.Proto, RcloneRemotes)
	}
}

func (uri URI) String() string {
	uriString := uri.Proto + "://"
	if uri.Component != "" {
		uriString += uri.Component + uri.Separator
	}
	if uri.Path != "" {
		uriString += uri.Path + uri.Separator
	}
	return uriString + uri.File
}

func (uri URI) CompletePath() string {
	return uri.Path + uri.Separator + uri.File
}

func (uri URI) Subpath(path string) URI {
	sub := uri
	sub.Path = path
	return sub
}
