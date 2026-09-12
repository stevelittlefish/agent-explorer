package main

import (
	"bytes"
	"embed"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed templates/*.html static/*
var assets embed.FS
var pages = template.Must(template.New("").Funcs(template.FuncMap{"rows": conversationRows, "prose": proseText, "base": filepath.Base, "blocks": textBlocks, "stamp": displayStamp, "date": func(t time.Time) string { return t.Local().Format("02 Jan 2006, 15:04") }}).ParseFS(assets, "templates/*.html"))

type Project struct {
	ID, Name string
	Count    int
	Updated  time.Time
}
type Page struct {
	Title, Agent, AgentName, Project, ProjectID string
	CSS                                         template.CSS
	Projects                                    []Project
	Chats                                       []Chat
	Chat                                        *Chat
	Export                                      bool
}
type App struct{ store *Store }

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	codexDefault := os.Getenv("CODEX_HOME")
	if codexDefault == "" {
		codexDefault = filepath.Join(home, ".codex")
	}
	claudeDefault := os.Getenv("CLAUDE_CONFIG_DIR")
	if claudeDefault == "" {
		claudeDefault = filepath.Join(home, ".claude")
	}
	addr := flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
	codex := flag.String("codex-dir", codexDefault, "Codex data directory")
	claude := flag.String("claude-dir", claudeDefault, "Claude Code data directory")
	flag.Parse()
	app := newApp(*codex, *claude)
	server := &http.Server{Addr: *addr, Handler: app, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	log.Printf("Agent Explorer: http://%s", *addr)
	log.Fatal(server.ListenAndServe())
}
func newApp(codex, claude string) *App {
	return &App{&Store{Roots: map[string][]string{"codex": {filepath.Join(codex, "sessions"), filepath.Join(codex, "archived_sessions")}, "claude": {filepath.Join(claude, "projects")}}}}
}
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; script-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", 405)
		return
	}
	if r.URL.Path == "/static/style.css" || r.URL.Path == "/static/app.js" {
		b, _ := assets.ReadFile(strings.TrimPrefix(r.URL.Path, "/"))
		if strings.HasSuffix(r.URL.Path, ".css") {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		} else {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		}
		w.Write(b)
		return
	}
	p := Page{Title: "Agent Explorer"}
	if r.URL.Path == "/" {
		render(w, p)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	names := map[string]string{"codex": "Codex", "claude": "Claude Code"}
	p.Agent = parts[0]
	p.AgentName = names[p.Agent]
	if p.AgentName == "" || !(len(parts) == 1 || len(parts) == 3 && parts[1] == "projects" || (len(parts) == 5 || len(parts) == 6) && parts[1] == "projects" && parts[3] == "chats") {
		http.NotFound(w, r)
		return
	}
	chats, err := a.store.List(p.Agent)
	if err != nil {
		log.Printf("list chats: %v", err)
		http.Error(w, "Could not read chat storage. Check directory permissions and server logs.", 500)
		return
	}
	p.Title = p.AgentName
	if len(parts) == 1 {
		byID := map[string]*Project{}
		for _, c := range chats {
			v := byID[c.ProjectID]
			if v == nil {
				v = &Project{ID: c.ProjectID, Name: c.Project, Updated: c.Updated}
				byID[c.ProjectID] = v
			}
			v.Count++
		}
		for _, v := range byID {
			p.Projects = append(p.Projects, *v)
		}
		sort.Slice(p.Projects, func(i, j int) bool { return p.Projects[i].Name < p.Projects[j].Name })
		render(w, p)
		return
	}
	p.ProjectID = parts[2]
	for _, c := range chats {
		if c.ProjectID == p.ProjectID {
			p.Project = c.Project
			p.Chats = append(p.Chats, c)
		}
	}
	if p.Project == "" {
		http.NotFound(w, r)
		return
	}
	p.Title = p.Project
	if len(parts) == 3 {
		render(w, p)
		return
	}
	for _, c := range p.Chats {
		if c.ID != parts[4] {
			continue
		}
		chat, err := readChat(c.Path, p.Agent, true)
		if err != nil {
			http.Error(w, "Could not read this chat.", 500)
			return
		}
		chat.Updated = c.Updated
		p.Chat = &chat
		p.Title = chat.Title
		if len(parts) == 6 {
			if parts[5] != "export" {
				http.NotFound(w, r)
				return
			}
			format := r.URL.Query().Get("format")
			if format == "" {
				format = "html"
			}
			if format != "html" && format != "txt" && format != "jsonl" {
				http.Error(w, "Unknown export format", 400)
				return
			}
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.%s"`, chat.ID, format))
			switch format {
			case "jsonl":
				w.Header().Set("Content-Type", "application/x-ndjson")
				http.ServeFile(w, r, chat.Path)
			case "txt":
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				fmt.Fprintf(w, "%s\n%s · %s\n\n", chat.Title, p.AgentName, chat.Project)
				for _, m := range chat.Messages {
					fmt.Fprintf(w, "--- %s %s ---\n%s\n\n", m.Role, m.Time, m.Text)
				}
			case "html":
				p.Export = true
				css, _ := assets.ReadFile("static/style.css")
				p.CSS = template.CSS(css) // Embedded application stylesheet; never transcript content.
				render(w, p)
			}
			return
		}
		render(w, p)
		return
	}
	http.NotFound(w, r)
}
func render(w http.ResponseWriter, p Page) {
	var b bytes.Buffer
	if err := pages.ExecuteTemplate(&b, "page", p); err != nil {
		log.Print(err)
		http.Error(w, "Could not render page", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b.Bytes())
}
