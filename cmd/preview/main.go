package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/andreasholmqvist/preview-publisher/internal/archive"
)

type clientConfig struct {
	serverURL string
	token     string
}

type preview struct {
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	PublicURL string    `json:"public_url"`
	Protected bool      `json:"protected"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	SizeBytes int64     `json:"size_bytes"`
	FileCount int       `json:"file_count"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "preview:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printRootHelp(os.Stdout)
		return nil
	}
	switch args[0] {
	case "-h", "--help", "help":
		printRootHelp(os.Stdout)
		return nil
	case "publish":
		return runPublish(args[1:])
	case "list":
		return runList(args[1:])
	case "delete":
		return runDelete(args[1:])
	case "password":
		return runPassword(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

type publishOptions struct {
	client       clientConfig
	path         string
	slug         string
	title        string
	passwordMode string
	password     string
}

func runPublish(args []string) error {
	opts, help, err := parsePublish(args)
	if err != nil {
		return err
	}
	if help {
		printPublishHelp(os.Stdout)
		return nil
	}
	if err := requireClient(opts.client); err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "preview-artifact-*.tar.gz")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	warnings, err := archive.CreateTarGz(opts.path, tmpPath)
	if err != nil {
		return err
	}
	for _, warning := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", warning)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := addFilePart(writer, "artifact", tmpPath); err != nil {
		return err
	}
	addField(writer, "slug", opts.slug)
	addField(writer, "title", opts.title)
	addField(writer, "password_mode", opts.passwordMode)
	addField(writer, "password", opts.password)
	if err := writer.Close(); err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, joinURL(opts.client.serverURL, "/api/previews"), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	addAuth(req, opts.client.token)

	var response struct {
		Slug      string `json:"slug"`
		PublicURL string `json:"public_url"`
		Protected bool   `json:"protected"`
		Created   bool   `json:"created"`
		Replaced  bool   `json:"replaced"`
		Password  string `json:"password"`
	}
	if err := doJSON(req, &response); err != nil {
		return err
	}
	fmt.Println(response.PublicURL)
	fmt.Println("slug:", response.Slug)
	if response.Password != "" {
		fmt.Println("password:", response.Password)
	}
	return nil
}

func runList(args []string) error {
	client, help, err := parseClientFlags(args, "list")
	if err != nil {
		return err
	}
	if help {
		printListHelp(os.Stdout)
		return nil
	}
	if err := requireClient(client); err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodGet, joinURL(client.serverURL, "/api/previews"), nil)
	if err != nil {
		return err
	}
	addAuth(req, client.token)
	var previews []preview
	if err := doJSON(req, &previews); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SLUG\tACCESS\tFILES\tBYTES\tUPDATED\tURL")
	for _, p := range previews {
		access := "public"
		if p.Protected {
			access = "protected"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\t%s\n", p.Slug, access, p.FileCount, p.SizeBytes, p.UpdatedAt.Format(time.RFC3339), p.PublicURL)
	}
	return tw.Flush()
}

func runDelete(args []string) error {
	client, rest, help, err := parseClientFlagsWithRest(args, "delete")
	if err != nil {
		return err
	}
	if help {
		printDeleteHelp(os.Stdout)
		return nil
	}
	if len(rest) != 1 {
		return errors.New("delete requires <slug>")
	}
	if err := requireClient(client); err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodDelete, joinURL(client.serverURL, "/api/previews/"+rest[0]), nil)
	if err != nil {
		return err
	}
	addAuth(req, client.token)
	if err := doNoBody(req); err != nil {
		return err
	}
	fmt.Println("deleted:", rest[0])
	return nil
}

func runPassword(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printPasswordHelp(os.Stdout)
		return nil
	}
	switch args[0] {
	case "set":
		return runPasswordSet(args[1:])
	case "remove":
		return runPasswordRemove(args[1:])
	default:
		return fmt.Errorf("unknown password command %q", args[0])
	}
}

func runPasswordSet(args []string) error {
	client, rest, help, err := parseClientFlagsWithRest(args, "password set")
	if err != nil {
		return err
	}
	if help {
		printPasswordSetHelp(os.Stdout)
		return nil
	}
	if len(rest) < 1 || len(rest) > 2 {
		return errors.New("password set requires <slug> [password]")
	}
	if err := requireClient(client); err != nil {
		return err
	}
	payload := map[string]string{}
	if len(rest) == 2 {
		payload["password"] = rest[1]
	}
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, joinURL(client.serverURL, "/api/previews/"+rest[0]+"/password"), &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	addAuth(req, client.token)
	var response struct {
		Password string `json:"password"`
	}
	if err := doJSON(req, &response); err != nil {
		return err
	}
	fmt.Println("password:", response.Password)
	return nil
}

func runPasswordRemove(args []string) error {
	client, rest, help, err := parseClientFlagsWithRest(args, "password remove")
	if err != nil {
		return err
	}
	if help {
		printPasswordRemoveHelp(os.Stdout)
		return nil
	}
	if len(rest) != 1 {
		return errors.New("password remove requires <slug>")
	}
	if err := requireClient(client); err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodDelete, joinURL(client.serverURL, "/api/previews/"+rest[0]+"/password"), nil)
	if err != nil {
		return err
	}
	addAuth(req, client.token)
	if err := doJSON(req, &struct{}{}); err != nil {
		return err
	}
	fmt.Println("password removed:", rest[0])
	return nil
}

func parsePublish(args []string) (publishOptions, bool, error) {
	opts := publishOptions{
		client: clientConfig{
			serverURL: os.Getenv("PREVIEW_GATEWAY_URL"),
			token:     os.Getenv("PREVIEW_GATEWAY_TOKEN"),
		},
		path:         ".",
		passwordMode: "none",
	}
	pathSet := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			return opts, true, nil
		case arg == "--server" || arg == "--token" || arg == "--slug" || arg == "--title":
			if i+1 >= len(args) {
				return opts, false, fmt.Errorf("%s requires a value", arg)
			}
			i++
			setPublishValue(&opts, arg, args[i])
		case strings.HasPrefix(arg, "--server="):
			opts.client.serverURL = strings.TrimPrefix(arg, "--server=")
		case strings.HasPrefix(arg, "--token="):
			opts.client.token = strings.TrimPrefix(arg, "--token=")
		case strings.HasPrefix(arg, "--slug="):
			opts.slug = strings.TrimPrefix(arg, "--slug=")
		case strings.HasPrefix(arg, "--title="):
			opts.title = strings.TrimPrefix(arg, "--title=")
		case arg == "--password":
			opts.passwordMode = "generated"
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				i++
				opts.passwordMode = "provided"
				opts.password = args[i]
			}
		case strings.HasPrefix(arg, "--password="):
			opts.passwordMode = "provided"
			opts.password = strings.TrimPrefix(arg, "--password=")
			if opts.password == "" {
				opts.passwordMode = "generated"
			}
		case strings.HasPrefix(arg, "-"):
			return opts, false, fmt.Errorf("unknown flag %s", arg)
		default:
			if pathSet {
				return opts, false, errors.New("publish accepts at most one path")
			}
			opts.path = arg
			pathSet = true
		}
	}
	return opts, false, nil
}

func setPublishValue(opts *publishOptions, flagName, value string) {
	switch flagName {
	case "--server":
		opts.client.serverURL = value
	case "--token":
		opts.client.token = value
	case "--slug":
		opts.slug = value
	case "--title":
		opts.title = value
	}
}

func parseClientFlags(args []string, command string) (clientConfig, bool, error) {
	client, rest, help, err := parseClientFlagsWithRest(args, command)
	if err != nil || help {
		return client, help, err
	}
	if len(rest) != 0 {
		return client, false, fmt.Errorf("%s does not accept positional arguments", command)
	}
	return client, false, nil
}

func parseClientFlagsWithRest(args []string, _ string) (clientConfig, []string, bool, error) {
	client := clientConfig{
		serverURL: os.Getenv("PREVIEW_GATEWAY_URL"),
		token:     os.Getenv("PREVIEW_GATEWAY_TOKEN"),
	}
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			return client, nil, true, nil
		case arg == "--server" || arg == "--token":
			if i+1 >= len(args) {
				return client, nil, false, fmt.Errorf("%s requires a value", arg)
			}
			i++
			if arg == "--server" {
				client.serverURL = args[i]
			} else {
				client.token = args[i]
			}
		case strings.HasPrefix(arg, "--server="):
			client.serverURL = strings.TrimPrefix(arg, "--server=")
		case strings.HasPrefix(arg, "--token="):
			client.token = strings.TrimPrefix(arg, "--token=")
		case strings.HasPrefix(arg, "-"):
			return client, nil, false, fmt.Errorf("unknown flag %s", arg)
		default:
			rest = append(rest, arg)
		}
	}
	return client, rest, false, nil
}

func requireClient(client clientConfig) error {
	if strings.TrimSpace(client.serverURL) == "" {
		return errors.New("missing server URL; set PREVIEW_GATEWAY_URL or pass --server")
	}
	if strings.TrimSpace(client.token) == "" {
		return errors.New("missing token; set PREVIEW_GATEWAY_TOKEN or pass --token")
	}
	return nil
}

func addFilePart(writer *multipart.Writer, fieldName, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	part, err := writer.CreateFormFile(fieldName, filepath.Base(path))
	if err != nil {
		return err
	}
	_, err = io.Copy(part, file)
	return err
}

func addField(writer *multipart.Writer, name, value string) {
	_ = writer.WriteField(name, value)
}

func addAuth(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
}

func doNoBody(req *http.Request) error {
	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return responseError(resp)
	}
	return nil
}

func doJSON(req *http.Request, output any) error {
	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return responseError(resp)
	}
	return json.NewDecoder(resp.Body).Decode(output)
}

func responseError(resp *http.Response) error {
	var body struct {
		Error string `json:"error"`
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err := json.Unmarshal(data, &body); err == nil && body.Error != "" {
		return fmt.Errorf("%s: %s", resp.Status, body.Error)
	}
	if len(data) > 0 {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	return errors.New(resp.Status)
}

func joinURL(base, path string) string {
	return strings.TrimRight(base, "/") + path
}

func printRootHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  preview publish [path] [flags]
  preview list [flags]
  preview delete <slug> [flags]
  preview password set <slug> [password] [flags]
  preview password remove <slug> [flags]

Environment:
  PREVIEW_GATEWAY_URL
  PREVIEW_GATEWAY_TOKEN
`)
}

func printPublishHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  preview publish [path] [flags]

Path defaults to the current directory. Directories must contain root index.html.
Single-file publish accepts one .html file and uploads it as index.html.

Flags:
  --server <url>       Preview gateway URL
  --token <token>      Admin API token
  --slug <slug>        Explicit preview slug
  --title <title>      Preview title
  --password [value]   Generate a password or use the provided value
`)
}

func printListHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  preview list [flags]

Flags:
  --server <url>   Preview gateway URL
  --token <token>  Admin API token
`)
}

func printDeleteHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  preview delete <slug> [flags]

Flags:
  --server <url>   Preview gateway URL
  --token <token>  Admin API token
`)
}

func printPasswordHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  preview password set <slug> [password] [flags]
  preview password remove <slug> [flags]
`)
}

func printPasswordSetHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  preview password set <slug> [password] [flags]

Omit password to ask the server to generate one.

Flags:
  --server <url>   Preview gateway URL
  --token <token>  Admin API token
`)
}

func printPasswordRemoveHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  preview password remove <slug> [flags]

Flags:
  --server <url>   Preview gateway URL
  --token <token>  Admin API token
`)
}
