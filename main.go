package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"text/template"

	"github.com/google/go-github/v68/github"
)

var (
	ctx      = context.Background()
	client   *github.Client
	reposDir = "repos"
	testsDir = "tests"
)

type suite struct {
	Path        string `json:"SuitePath"`
	Description string `json:"SuiteDescription"`
	Succeeded   bool   `json:"SuiteSucceeded"`
	PreRunStats struct {
		TotalSpecs       int `json:"TotalSpecs"`
		SpecsThatWillRun int `json:"SpecsThatWillRun"`
	} `json:"PreRunStats"`
	SpecReports []spec
}

type spec struct {
	Name     string `json:"LeafNodeText"`
	Type     string `json:"LeafNodeType"`
	State    string `json:"State"`
	Attempts int    `json:"NumAttempts"`
	Failure  struct {
		Message  string `json:"Message"`
		Location struct {
			LineNumber int    `json:"LineNumber"`
			StackTrace string `json:"FullStackTrace"`
		} `json:"Location"`
	}
}

func ifErr(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func clone(repo string) error {
	s := strings.Split(repo, "/")
	if len(s) != 2 {
		return fmt.Errorf("invalid repo format: %s", repo)
	}
	r, _, err := client.Repositories.Get(ctx, s[0], s[1])
	if err != nil {
		return err
	}
	fmt.Printf("Cloning repository: %s\n", *r.FullName)
	dirName := strings.ReplaceAll(repo, "/", "_")
	cmd := exec.Command("git", "clone", "--depth=1", *r.CloneURL, fmt.Sprintf("%s/%s", reposDir, dirName))
	return cmd.Run()
}

func runTests(repos []string) {
	cwd, err := os.Getwd()
	ifErr(err)
	outputDir := fmt.Sprintf("--output-dir=%s/%s", cwd, testsDir)

	for _, repo := range repos {
		dir := fmt.Sprintf("%s/%s/test/e2e", reposDir, strings.ReplaceAll(repo, "/", "_"))
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			fmt.Printf("[SKIP] No test/e2e directory for %s\n", repo)
			continue
		}
		fmt.Printf("Running tests for %s...\n", repo)

		cmd := exec.Command(
			"ginkgo",
			"--tags=e2e,osde2e",
			"--flake-attempts=3",
			"--procs=4",
			"-vv",
			"--trace",
			outputDir,
			fmt.Sprintf("--json-report=%s.json", strings.ReplaceAll(repo, "/", "_")),
			".",
		)
		cmd.Dir = dir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Printf("[ERROR] Ginkgo test failed for %s: %v\n", repo, err)
		}
	}
}

func generateReport() {
	entries, err := os.ReadDir(testsDir)
	ifErr(err)

	f, err := os.Create("report.txt")
	ifErr(err)
	defer f.Close()

	tpl := template.Must(template.New("suite.tpl").ParseFiles("suite.tpl"))

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var s []suite
		b, err := os.ReadFile(fmt.Sprintf("%s/%s", testsDir, entry.Name()))
		if err != nil {
			fmt.Println(err)
			continue
		}
		if err := json.Unmarshal(b, &s); err != nil {
			fmt.Println(err)
			continue
		}
		ifErr(tpl.Execute(f, s))
	}
}

func main() {
	flag.Parse()

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		log.Fatal("[ERROR] GITHUB_TOKEN must be set in the environment")
	}
	client = github.NewClient(nil).WithAuthToken(token)

	ifErr(os.MkdirAll(reposDir, os.ModePerm))
	ifErr(os.MkdirAll(testsDir, os.ModePerm))

	b, err := io.ReadAll(os.Stdin)
	ifErr(err)

	var repos []string
	for _, line := range strings.Split(string(b), "\n") {
		repo := strings.TrimSpace(line)
		if repo != "" {
			repos = append(repos, repo)
		}
	}

	var wg sync.WaitGroup
	for _, repo := range repos {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			ifErr(clone(r))
		}(repo)
	}
	wg.Wait()

	runTests(repos)
	generateReport()

	fmt.Println(" All done! See report.txt for results.")
}

