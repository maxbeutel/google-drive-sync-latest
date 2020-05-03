package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"time"

	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
)

// DownloadFile downloads the content of a given file object
func DownloadFile(d *drive.Service, t http.RoundTripper, f *drive.File) (string, error) {
	// t parameter should use an oauth.Transport
	downloadURL := f.WebContentLink
	if downloadURL == "" {
		// If there is no downloadURL, there is no body
		fmt.Printf("An error occurred: File is not downloadable")
		return "", nil
	}
	req, err := http.NewRequest("GET", downloadURL, nil)
	if err != nil {
		fmt.Printf("An error occurred: %v\n", err)
		return "", err
	}
	resp, err := t.RoundTrip(req)
	// Make sure we close the Body later
	defer resp.Body.Close()
	if err != nil {
		fmt.Printf("An error occurred: %v\n", err)
		return "", err
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("An error occurred: %v\n", err)
		return "", err
	}
	return string(body), nil
}

// Retrieve a token, saves the token, then returns the generated client.
func getClient(config *oauth2.Config) *http.Client {
	// The file token.json stores the user's access and refresh tokens, and is
	// cpreated automatically when the authorization flow completes for the first
	// time.
	tokFile := "token.json"
	tok, err := tokenFromFile(tokFile)
	if err != nil {
		tok = getTokenFromWeb(config)
		saveToken(tokFile, tok)
	}
	return config.Client(context.Background(), tok)
}

// Request a token from the web, then returns the retrieved token.
func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser then type the "+
		"authorization code: \n%v\n", authURL)

	var authCode string
	if _, err := fmt.Scan(&authCode); err != nil {
		log.Fatalf("Unable to read authorization code %v", err)
	}

	tok, err := config.Exchange(context.TODO(), authCode)
	if err != nil {
		log.Fatalf("Unable to retrieve token from web %v", err)
	}
	return tok
}

// Retrieves a token from a local file.
func tokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tok := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(tok)
	return tok, err
}

// Saves a token to a file path.
func saveToken(path string, token *oauth2.Token) {
	fmt.Printf("Saving credential file to: %s\n", path)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		log.Fatalf("Unable to cache oauth token: %v", err)
	}
	defer f.Close()
	json.NewEncoder(f).Encode(token)
}

func main() {
	if len(os.Args) != 4 {
		fmt.Println("Usage:", os.Args[0], "SRC_DIR", "TARGET_DIR", "CRED_FILE")
		return
	}

	srcDir := os.Args[1]
	targetDir := os.Args[2]
	credFile := os.Args[3]

	log.Println("Arguments:", srcDir, targetDir, credFile)

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		log.Fatalf("Unable to create target dir: %v", err)
	}

	b, err := ioutil.ReadFile(credFile)
	if err != nil {
		log.Fatalf("Unable to read client secret file: %v", err)
	}

	// If modifying these scopes, delete your previously saved token.json.
	config, err := google.ConfigFromJSON(b, drive.DriveReadonlyScope)
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}
	client := getClient(config)

	srv, err := drive.New(client)
	if err != nil {
		log.Fatalf("Unable to retrieve Drive client: %v", err)
	}

	folders, err := srv.Files.List().
		Q(fmt.Sprintf("mimeType = 'application/vnd.google-apps.folder' and name = '%s'", srcDir)).
		PageSize(1).
		Fields("files(id, name)").Do()

	if err != nil {
		log.Fatalf("Unable to retrieve folders: %v", err)
	}

	if len(folders.Files) == 0 {
		log.Fatalf("No folders found.")
	}

	folder := folders.Files[0]
	log.Println("Found folder", folder.Name, folder.Id)

	files, err := srv.Files.List().
		Q(fmt.Sprintf("'%s' in parents", folder.Id)).
		PageSize(25).
		OrderBy("createdTime desc").
		Fields("files(id, name, modifiedTime)").
		Do()

	if err != nil {
		log.Fatalf("Unable to retrieve files: %v", err)
	}

	if len(files.Files) == 0 {
		log.Fatalf("No files found.")
	}

	for _, f := range files.Files {
		log.Println("Found file", f.Name, f.Id, f.CreatedTime)

		outName := targetDir + "/" + cleanFilename(f.Name)

		log.Println(">> Outfile name is", outName)

		if fileExists(outName) {
			log.Println(">> File already exists", outName)
			continue
		}

		log.Println(">> Downloading to", outName)

		resp, err := srv.Files.Get(f.Id).Download()

		if err != nil {
			log.Println(">> Failed to download")
			continue
		}

		log.Println(">> Download response OK")

		out, err := os.Create(outName)

		if err != nil {
			log.Println(">> Failed to create filename", err, outName)

			resp.Body.Close()
			continue
		}

		io.Copy(out, resp.Body)

		resp.Body.Close()
		out.Close()

		log.Println("mtime", f.ModifiedTime)

		t, err := time.Parse(time.RFC3339Nano, f.ModifiedTime)

		if err != nil {
			log.Println(">> WARN: Failed to parse modified time")
		} else {
			if err := os.Chtimes(outName, t, t); err != nil {
				log.Println(">> WARN: Failed to change creation time")
			}
		}

		log.Println(">> Storing as file OK")
	}
}

func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

var re = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func cleanFilename(in string) string {
	return re.ReplaceAllString(in, "_")
}
