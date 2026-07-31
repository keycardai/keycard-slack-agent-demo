package main

import (
	"bufio"
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/slack-go/slack"

	"github.com/keycardai/keycard-slack-agent-demo/agent"
	"github.com/keycardai/keycard-slack-agent-demo/config"
	"github.com/keycardai/keycard-slack-agent-demo/crypto"
	"github.com/keycardai/keycard-slack-agent-demo/db"
	"github.com/keycardai/keycard-slack-agent-demo/discovery"
	"github.com/keycardai/keycard-slack-agent-demo/oauth"
	"github.com/keycardai/keycard-slack-agent-demo/prompts"
	"github.com/keycardai/keycard-slack-agent-demo/slackapp"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	dev := flag.Bool("dev", false, "load .env.local before startup")
	flag.Parse()
	if *dev {
		if err := loadEnvFile(".env.local", true); err != nil {
			log.Printf("env load skipped: %v", err)
		}
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	cipher, err := crypto.NewCipher(cfg.EncryptionKey)
	if err != nil {
		log.Fatalf("cipher error: %v", err)
	}

	database, err := db.Open(cfg.DatabasePath, cipher)
	if err != nil {
		log.Fatalf("db error: %v", err)
	}
	defer database.Close()

	keycardClient := oauth.NewClient(cfg.KeycardZoneURL, cfg.KeycardClientID, cfg.KeycardClientSecret)
	if cfg.AnthropicAPIKey != "" {
		_ = os.Setenv("ANTHROPIC_API_KEY", cfg.AnthropicAPIKey)
	}
	anthropicClient := anthropic.NewClient()

	slackClient := slack.New(
		cfg.SlackBotToken,
		slack.OptionAppLevelToken(cfg.SlackAppToken),
	)

	// One signer mints consent links and verifies them, so a link the agent did not create cannot
	// be redeemed.
	stateSigner := oauth.NewStateSigner(cfg.StateSecret)

	oauthHandler := &oauth.Handler{
		Config:       cfg,
		DB:           database,
		Signer:       stateSigner,
		Client:       keycardClient,
		SlackActions: &slackapp.ActionClient{Slack: slackClient},
	}

	agentRunner := agent.NewRunner(
		database,
		&anthropicClient,
		prompts.System,
	)
	agentRunner.Model = cfg.AnthropicModel
	agentRunner.LinkSigner = agentLinkSigner{stateSigner}

	agentRunner.Discovery = discovery.NewService(
		cfg.KeycardZoneID,
		cfg.KeycardAPIBaseURL,
		cfg.KeycardServiceAccountID,
		cfg.KeycardServiceAccountSecret,
	)
	resourceBroker, err := oauth.NewTokenExchangeBroker(cfg.KeycardZoneURL, cfg.KeycardClientID, cfg.KeycardClientSecret, keycardClient.HTTP)
	if err != nil {
		log.Fatalf("resource broker error: %v", err)
	}
	agentRunner.ResourceBroker = resourceBroker
	agentRunner.SlackActions = &slackapp.ActionClient{Slack: slackClient}
	agentRunner.BaseURL = cfg.BaseURL
	oauthHandler.Onboarder = agentRunner

	slackHandler := slackapp.NewHandler(cfg, database, agentRunner, keycardClient, slackClient, cfg.BaseURL)
	slackHandler.LinkSigner = stateSigner

	slackService := slackapp.NewService(slackClient, slackHandler)

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/start", oauthHandler.Start)
	mux.HandleFunc("/oauth/callback", oauthHandler.Callback)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		log.Printf("http server listening on %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server error: %v", err)
		}
	}()

	go func() {
		if err := slackService.Run(ctx); err != nil {
			log.Printf("slack socket error: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
	log.Println("shutdown complete")
}

func loadEnvFile(path string, override bool) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	loaded := 0
	scanner := bufio.NewScanner(file)
	var pending string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if pending != "" {
			line = pending + line
			pending = ""
		}
		for strings.HasSuffix(line, "\\") {
			line = strings.TrimSuffix(line, "\\")
			if !scanner.Scan() {
				break
			}
			next := strings.TrimRight(scanner.Text(), "\r\n")
			line += next
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		if key == "" {
			continue
		}
		if !override && os.Getenv(key) != "" {
			continue
		}
		val = strings.Trim(val, "\"'")
		if err := os.Setenv(key, val); err == nil {
			loaded++
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if loaded > 0 {
		log.Printf("env loaded: %s (%d keys)", path, loaded)
	}
	return nil
}

// agentLinkSigner adapts the OAuth state signer to the narrower interface the agent package declares,
// so the agent does not need to know the shape of an OAuth link request.
type agentLinkSigner struct {
	signer *oauth.StateSigner
}

func (a agentLinkSigner) SignLink(teamID, userID, channelID string, resources []string) (string, error) {
	return a.signer.SignLink(oauth.LinkRequest{
		TeamID:    teamID,
		UserID:    userID,
		ChannelID: channelID,
		Resources: resources,
	})
}
