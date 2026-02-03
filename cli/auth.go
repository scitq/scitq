package cli

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"

	"golang.org/x/term"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
	"github.com/scitq/scitq/lib"
	"google.golang.org/protobuf/types/known/emptypb"
)

func promptCredentials() (string, string, error) {
	if runtime.GOOS != "windows" {
		// Unix logic (as before)
		tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, "❌ No terminal available for login prompt.")
			os.Exit(1)
		}
		defer tty.Close()

		fmt.Fprint(tty, "🔐 Username: ")
		reader := bufio.NewReader(tty)
		username, err := reader.ReadString('\n')
		if err != nil {
			return "", "", fmt.Errorf("failed to read username: %w", err)
		}
		username = strings.TrimSpace(username)

		fmt.Fprint(tty, "🔑 Password: ")
		passwordBytes, err := term.ReadPassword(int(tty.Fd()))
		fmt.Fprintln(tty)
		if err != nil {
			return "", "", fmt.Errorf("failed to read password: %w", err)
		}

		return username, string(passwordBytes), nil
	}

	// Windows: use ReadConsoleW to print and read interactively
	username, err := readConsoleLine("🔐 Username: ")
	if err != nil {
		return "", "", err
	}
	password, err := readConsolePassword("🔑 Password: ")
	if err != nil {
		return "", "", err
	}
	return username, password, nil
}

func getToken() (string, error) {
	token := os.Getenv("SCITQ_TOKEN")
	if token == "" {
		return "", fmt.Errorf("please execute export SCITQ_TOKEN=$(%s login) before running this command", os.Args[0])
	}
	return token, nil
}

func createToken(serverAddr string, user, userPassword *string) string {

	qcclient, err := lib.CreateLoginClient(serverAddr)
	if err != nil {
		log.Fatal("failed to create client: %w", err)
	}
	defer qcclient.Close()

	client := qcclient.Client
	username, password := "", ""

	if user == nil || userPassword == nil {
		// Prompt user for login
		username, password, err = promptCredentials()
		if err != nil {
			log.Fatal("failed to read credentials: %w", err)
		}
	} else {
		username, password = *user, *userPassword
	}

	resp, err := client.Login(context.Background(), &pb.LoginRequest{
		Username: username,
		Password: password,
	})
	if err != nil {
		log.Fatalf("login failed: %v (%s)", err, password)
	}

	// Store token for current session only
	token := resp.Token
	if err != nil {
		log.Fatal("failed to set token env: %w", err)
	}

	return token
}

func fetchCertificate(serverAddr string) string {
	qcclient, err := lib.CreateLoginClient(serverAddr)
	if err != nil {
		log.Fatal("failed to create client: %w", err)
	}
	defer qcclient.Close()

	client := qcclient.Client
	resp, err := client.GetCertificate(context.Background(), &emptypb.Empty{})
	if err != nil {
		log.Fatalf("failed to fetch certificate: %v", err)
	}

	return resp.Pem
}
