package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
	"path/filepath"
	"os"
	"github.com/jlaffaye/ftp"
)

const (
	serverPort = "8080" //Replace with your port
	ftpHost = "10.1.204.16" //Replace with your android host ip
	ftpPort = "1986" //Replace with your android host port
	ftpUser = "pc" //Replace with your android host user
	ftpPass = "0000" //Replace with your android host password
	downloadDir = "./downloads" // Local directory to store downloaded files
)


func init() {
	// Create downloads directory if it doesn't exist
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		log.Fatalf("Failed to create downloads directory: %v", err)
	}
}

func connectFTP() (*ftp.ServerConn, error) {
	addr := fmt.Sprintf("%s:%s", ftpHost, ftpPort)
	c, err := ftp.Dial(addr, ftp.DialWithTimeout(5*time.Second))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to FTP server: %v", err)
	}

	err = c.Login(ftpUser, ftpPass)
	if err != nil {
		c.Quit()
		return nil, fmt.Errorf("failed to login: %v", err)
	}

	return c, nil
}

func main() {
	// Test FTP connection
	conn, err := connectFTP()
	if err != nil {
		log.Fatalf("Failed to establish FTP connection: %v", err)
	}
	
	entries, err := conn.List("")
	if err != nil {
		log.Printf("Warning: Could not list directory: %v", err)
	} else {
		log.Printf("Directory contents:")
		for _, entry := range entries {
			log.Printf("- %s (%d bytes)", entry.Name, entry.Size)
		}
	}
	conn.Quit()

	http.HandleFunc("/", handleFileRequest)
	http.HandleFunc("/download/", handleDownload)
	http.HandleFunc("/upload", handleUpload)
	
	fmt.Printf("Server starting on http://localhost:%s\n", serverPort)
	fmt.Printf("Connected to FTP server at %s:%s\n", ftpHost, ftpPort)
	fmt.Printf("Downloads will be saved to: %s\n", downloadDir)
	
	log.Fatal(http.ListenAndServe(":"+serverPort, nil))
}

func handleFileRequest(w http.ResponseWriter, r *http.Request) {
	requestPath := r.URL.Path
	requestPath = strings.TrimPrefix(requestPath, "/")
	if requestPath == "" {
		requestPath = ""
	}

	log.Printf("Accessing path: '%s'", requestPath)

	conn, err := connectFTP()
	if err != nil {
		http.Error(w, "Error connecting to FTP server: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer conn.Quit()

	entries, err := conn.List(requestPath)
	if err == nil {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `
			<html>
			<head>
				<style>
					body { font-family: Arial, sans-serif; margin: 20px; }
					.file-list { list-style: none; padding: 0; }
					.file-list li { margin: 10px 0; padding: 5px; border-bottom: 1px solid #eee; }
					.upload-form { margin: 20px 0; padding: 10px; background: #f5f5f5; }
				</style>
			</head>
			<body>
			<h1>Directory: /%s</h1>
		`, requestPath)

		// Upload form
		fmt.Fprintf(w, `
			<div class="upload-form">
				<h3>Upload File</h3>
				<form action="/upload" method="post" enctype="multipart/form-data">
					<input type="hidden" name="path" value="%s">
					<input type="file" name="file" required>
					<input type="submit" value="Upload">
				</form>
			</div>
			<ul class="file-list">
		`, requestPath)
		
		if requestPath != "" {
			fmt.Fprintf(w, `<li><a href="/%s">..</a></li>`, getParentPath(requestPath))
		}

		for _, entry := range entries {
			name := entry.Name
			if name == "." || name == ".." {
				continue
			}
			
			path := joinPath(requestPath, name)
			displayName := name
			if entry.Type == ftp.EntryTypeFolder {
				displayName += "/"
				fmt.Fprintf(w, `<li>📁 <a href="/%s">%s</a> (%d bytes)</li>`, path, displayName, entry.Size)
			} else {
				fmt.Fprintf(w, `
					<li>📄 <a href="/%s">%s</a> (%d bytes)
						<a href="/download/%s" style="margin-left: 10px;">[Download]</a>
					</li>
				`, path, displayName, entry.Size, path)
			}
		}
		fmt.Fprintf(w, "</ul></body></html>")
		return
	}

	// If not a directory, try to download file
	resp, err := conn.Retr(requestPath)
	if err != nil {
		log.Printf("Error retrieving file '%s': %v", requestPath, err)
		http.Error(w, "File not found: "+err.Error(), http.StatusNotFound)
		return
	}
	defer resp.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(requestPath)))
	
	_, err = io.Copy(w, resp)
	if err != nil {
		log.Printf("Error copying file '%s': %v", requestPath, err)
	}
}

func handleDownload(w http.ResponseWriter, r *http.Request) {
	filePath := strings.TrimPrefix(r.URL.Path, "/download/")
	
	conn, err := connectFTP()
	if err != nil {
		http.Error(w, "Error connecting to FTP server: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer conn.Quit()

	resp, err := conn.Retr(filePath)
	if err != nil {
		http.Error(w, "Error retrieving file: "+err.Error(), http.StatusNotFound)
		return
	}
	defer resp.Close()

	// Create local file
	localPath := filepath.Join(downloadDir, filepath.Base(filePath))
	out, err := os.Create(localPath)
	if err != nil {
		http.Error(w, "Error creating local file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer out.Close()

	// Copy file content
	_, err = io.Copy(out, resp)
	if err != nil {
		http.Error(w, "Error saving file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, `
		<html><body>
		<h2>File Downloaded Successfully!</h2>
		<p>File saved to: %s</p>
		<p><a href="/%s">Back to directory</a></p>
		</body></html>
	`, localPath, filepath.Dir(filePath))
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse the multipart form
	err := r.ParseMultipartForm(10 << 20) // 10 MB max
	if err != nil {
		http.Error(w, "Error parsing form: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Error getting file: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	path := r.FormValue("path")
	targetPath := joinPath(path, header.Filename)

	conn, err := connectFTP()
	if err != nil {
		http.Error(w, "Error connecting to FTP server: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer conn.Quit()

	err = conn.Stor(targetPath, file)
	if err != nil {
		http.Error(w, "Error uploading file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/"+path, http.StatusSeeOther)
}

func getParentPath(path string) string {
	if path == "" {
		return ""
	}
	path = strings.TrimRight(path, "/")
	lastSlash := strings.LastIndex(path, "/")
	if lastSlash < 0 {
		return ""
	}
	return path[:lastSlash]
}

func joinPath(base, path string) string {
	if base == "" {
		return path
	}
	return base + "/" + path
} 