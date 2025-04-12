package cli

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"

	"golang.org/x/term"

	pb "github.com/gmtsciencedev/scitq2/gen/taskqueuepb"
	"github.com/gmtsciencedev/scitq2/lib"
)

func promptCredentials() (string, string, error) {
	// Open /dev/tty to get a real terminal even in a subshell
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		fmt.Println("❌ No terminal available for login prompt.")
		fmt.Println("   Please run interactively or pass credentials another way.")
		os.Exit(1)
	}
	defer tty.Close()

	fmt.Fprint(tty, "🔐 Username: ")
	reader := bufio.NewReader(tty)

	username, err := reader.ReadString('\n')
	if err != nil {
		return "", "", fmt.Errorf("failed to read username: %w", err)
	}

	fmt.Fprint(tty, "🔑 Password: ")
	fd := int(tty.Fd())
	passwordBytes, err := term.ReadPassword(fd)
	fmt.Fprintln(tty) // newline after password
	if err != nil {
		return "", "", fmt.Errorf("failed to read password: %w", err)
	}

	return username[:len(username)-1], string(passwordBytes), nil
}

func getToken() (string, error) {
	token := os.Getenv("SCITQ_TOKEN")
	if token == "" {
		return "", fmt.Errorf("please execute export SCITQ_TOKEN=$(%s login) before running this command", os.Args[0])
	}
	return token, nil
}

func createToken(serverAddr string) string {

	qcclient, err := lib.CreateLoginClient(serverAddr)
	if err != nil {
		log.Fatal("failed to create client: %w", err)
	}
	defer qcclient.Close()

	client := qcclient.Client

	// Prompt user for login
	username, password, err := promptCredentials()
	if err != nil {
		log.Fatal("failed to read credentials: %w", err)
	}

	resp, err := client.Login(context.Background(), &pb.LoginRequest{
		Username: username,
		Password: password,
	})
	if err != nil {
		log.Fatal("login failed: %w", err)
	}

	// Store token for current session only
	token := resp.Token
	if err != nil {
		log.Fatal("failed to set token env: %w", err)
	}

	return token
}
