package install

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	pb "github.com/scitq/scitq/gen/taskqueuepb"
)

type Reporter func(prog int, msg string)

const DockerCredentialFile = "/root/.docker/config.json"

func checkScratch() error {
	log.Printf("Checking scratch")

	log.Printf("Checking /scratch folder")
	_, err := executeShellCommand("mkdir -p /scratch")
	if err != nil {
		return err
	}

	// check for nvme
	log.Printf("Looking for NVMe disks")
	nvme, err := countCommand("ls /dev/nvme*n1|wc -l")
	if err != nil {
		return err
	}
	if nvme > 0 {
		mountnvme, err := countCommand("mount|grep -E nvme.n1|wc -l")
		if err != nil {
			return err
		}
		if mountnvme > 0 {
			log.Printf("NVMe disks already mounted, giving up")
		} else {
			log.Printf("NVMe disks found and unmounted, looking for MD array")
			command := "mdadm --examine --scan "
			for i := 0; i < nvme; i++ {
				command += fmt.Sprintf("| grep nvme%dn1 ", i)
			}
			command += "|wc -l"

			hasMdArray, err := countCommand(command)
			if err != nil {
				return err
			}
			if hasMdArray == 0 {
				command = fmt.Sprintf("mdadm --create /dev/md127 --force --level=0 --raid-devices=%d ", nvme)
				for i := 0; i < nvme; i++ {
					command += fmt.Sprintf("/dev/nvme%dn1 ", i)
				}
				err = executeCommands(3, command)
				if err != nil {
					return err
				}
				err = executeCommands(3, "mdadm --detail --scan > /etc/mdadm/mdadm.conf", "mkfs.ext4 /dev/md127", "tune2fs -m 0 /dev/md127")
				if err != nil {
					return err
				}
			}

			mdArrayIsMounted, err := countCommand("findmnt -n /dev/md127 |wc -l")
			if err != nil {
				return err
			}
			if mdArrayIsMounted == 0 {
				err = executeCommands(3, "mount /dev/md127 /scratch")
				if err != nil {
					return err
				}
			}
		}
	} else {
		log.Printf("NVMe disks not found, looking for /mnt partition")
		// testing if mnt is bigger than root
		mntBigger, err := executeShellCommand("echo $([ $(df -k --output=size /mnt|sed '1 d') -gt $(df -k --output=size /|sed '1 d') ] && echo true)")
		if err != nil {
			return err
		}
		if mntBigger == "true\n" {
			log.Printf("/mnt is bigger than root, using it for scratch")
			err = executeCommands(3, "mount --bind /mnt /scratch")
			if err != nil {
				return err
			}
		}
	}

	log.Printf("Setting permission on /scratch")
	_, err = executeShellCommand("chmod a+rwx /scratch")
	if err != nil {
		return err
	}

	return nil
}

// test if docker is installed and install it if it is not present
func checkDocker() error {
	// testing for docker
	log.Printf("Checking docker")

	log.Printf("Checking /scratch/docker")
	_, err := executeShellCommand("mkdir -p /scratch/docker")
	if err != nil {
		return err
	}

	scratchDockerIsMounted, err := countCommand("findmnt -n /scratch/docker |wc -l")
	if err != nil {
		return err
	}

	// Two orthogonal signals:
	//   serviceActive — is the docker daemon currently running? Drives
	//     the stop/start dance around the bind-mount. The previous
	//     `isPackageInstalled("docker")` was wrong on the Microsoft
	//     Ubuntu HPC 22.04 image (default for has_gpu recruits): the
	//     package shipped under a name dpkg's grep didn't match the
	//     way we expected, so hasDocker came back false, we SKIPPED
	//     the stop+start, bind-mounted on top of a running daemon's
	//     data-root, and the daemon then failed to keep serving the
	//     socket — first `docker pull` failed with "Cannot connect
	//     to the Docker daemon" (alpha2 incident 2026-06-25, worker
	//     6111). The service check is the right gate.
	//   pkgInstalled — only matters for fresh CPU images where the
	//     `apt install docker.io` branch needs to know the package is
	//     missing. A running service implies the package exists, so
	//     pkgInstalled is only consulted in the not-active branch.
	log.Printf("Checking docker service")
	serviceActive, err := isServiceActive("docker")
	if err != nil {
		return err
	}
	pkgInstalled := serviceActive
	if !serviceActive {
		pkgInstalled, err = isPackageInstalled("docker")
		if err != nil {
			return err
		}
	}

	if scratchDockerIsMounted == 0 {
		if serviceActive {
			_, err = executeShellCommand("systemctl stop docker")
			if err != nil {
				return err
			}
		}
		log.Printf("Mounting /scratch/docker")
		err = executeCommands(3, "mkdir -p /var/lib/docker", "mount --bind /scratch/docker /var/lib/docker")
		if err != nil {
			return err
		}
		if serviceActive {
			// Restart docker after the bind-mount swapped its
			// data-root. The previous version returned as soon as
			// systemctl reported the start submission accepted —
			// good enough on idle workers, but the first
			// download-phase `docker pull` on a fast-scheduled
			// task can land before the daemon socket exists. Poll
			// for readiness before declaring "docker: ok".
			_, err = executeShellCommand("systemctl start docker")
			if err != nil {
				return err
			}
			if err := waitForDockerReady(30 * time.Second); err != nil {
				return fmt.Errorf("docker restart after bind-mount: %w", err)
			}
		}
	}

	if !pkgInstalled {
		log.Printf("Installing docker")
		err = executeCommands(5,
			"apt update && apt install -y docker.io")
		if err != nil {
			return err
		}
		// apt install starts the service via the package's
		// post-install hook; same readiness probe as above so we
		// don't report "docker: ok" before the socket exists.
		if err := waitForDockerReady(30 * time.Second); err != nil {
			return fmt.Errorf("docker install: %w", err)
		}
	}

	return nil
}

// waitForDockerReady polls `docker info` until it succeeds or the
// timeout elapses. Used after systemctl start / apt install to confirm
// the daemon socket is actually serving — `systemctl start` returns as
// soon as systemd accepts the start request, which can be seconds
// ahead of the daemon being reachable on /var/run/docker.sock.
func waitForDockerReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		_, err := executeShellCommand("docker info >/dev/null 2>&1")
		if err == nil {
			log.Printf("docker daemon is reachable")
			return nil
		}
		lastErr = err
		time.Sleep(time.Second)
	}
	return fmt.Errorf("docker info failed for %s: %w", timeout, lastErr)
}

func checkSwap(swapProportion float32) error {
	log.Printf("Checking /scratch/swapfile")
	if fileNotExist("/scratch/swapfile") {
		log.Printf("Creating swap file")
		swapSize, err := countCommand(fmt.Sprintf("df -k --output=size /scratch|perl -ne 'print int($_*%f) if /^\\s*[0-9]+$/'", swapProportion))
		if err != nil {
			return err
		}
		err = executeCommands(3, fmt.Sprintf("fallocate -l \"%d\"k /scratch/swapfile", swapSize),
			"chmod 600 /scratch/swapfile",
			"mkswap /scratch/swapfile")
		if err != nil {
			return err
		}
	}

	log.Printf("Checking if swap file enabled")
	isSwapFileInUse, err := countCommand("swapon --show | grep '/scratch/swapfile' |wc -l")
	if err != nil {
		return err
	}
	if isSwapFileInUse == 0 {
		log.Printf("Activating swap file")
		_, err = executeShellCommand("swapon /scratch/swapfile")
	}

	return err
}

func disableAptDaily() error {
	log.Printf("Disabling apt daily services/timers")
	return executeCommands(3,
		"systemctl disable --now apt-daily.service apt-daily.timer",
		"systemctl disable --now apt-daily-upgrade.service apt-daily-upgrade.timer",
	)
}

func checkService(swapProportion float32, serverAddr string, concurrency int, token string) error {

	if fileNotExist("/etc/systemd/system/scitq-client.service") {
		err := writeFile("/etc/systemd/system/scitq-client.service", fmt.Sprintf(`[Unit]
Description=scitq-client
After=multi-user.target

[Service]
Environment=PATH=/usr/bin:/usr/local/bin:/usr/sbin
Type=simple
KillSignal=SIGTERM
TimeoutStopSec=1h
# Restart=on-failure (not always): preserves clean exit 0 as
# "deliberately stopped, stay stopped" -- e.g. operator running
# kill -TERM directly. The worker upgrade flow uses exit code 75
# (EX_TEMPFAIL) so respawn-on-upgrade still works under this
# policy. See specs/worker_autoupgrade.md.
Restart=on-failure
RestartSec=5
ExecStart=/usr/local/bin/scitq-client -server %s -install -swap "%f" -concurrency %d -token %s

[Install]
WantedBy=multi-user.target`, serverAddr, swapProportion, concurrency, token), false)
		if err != nil {
			return fmt.Errorf("could not create service %w", err)
		}
		err = executeCommands(3, "systemctl daemon-reload", "systemctl enable scitq-client")
		return err
	} else {
		_, err := executeShellCommand("systemctl is-enabled scitq-client")
		if err != nil {
			err = executeCommands(3, "systemctl daemon-reload", "systemctl enable scitq-client")
		}
		return err
	}

}

func InstallRcloneConfig(rcloneConfig, rcloneConfigPath string) error {
	if fileNotExist(rcloneConfigPath) {
		if err := writeFile(rcloneConfigPath, rcloneConfig, false); err != nil {
			return fmt.Errorf("could not create rclone config: %w", err)
		}
	}
	return nil
}

// InstallDockerCredentials writes /root/.docker/config.json atomically from the server-provided proto.
func InstallDockerCredentials(creds *pb.DockerCredentials) error {
	if creds == nil || len(creds.Credentials) == 0 {
		log.Printf("No docker credentials provided by server; skipping docker config installation")
		return nil
	}

	type authEntry struct {
		Auth string `json:"auth"`
	}
	cfg := struct {
		Auths map[string]authEntry `json:"auths"`
	}{Auths: map[string]authEntry{}}

	for _, c := range creds.Credentials {
		reg := c.GetRegistry()
		auth := c.GetAuth()
		if reg == "" || auth == "" {
			continue
		}
		cfg.Auths[reg] = authEntry{Auth: auth}
	}
	if len(cfg.Auths) == 0 {
		log.Printf("No valid docker credentials in response; skipping docker config installation")
		return nil
	}

	dir := filepath.Dir(DockerCredentialFile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal docker config: %w", err)
	}
	tmp := DockerCredentialFile + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write tmp docker config: %w", err)
	}
	if err := os.Rename(tmp, DockerCredentialFile); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename docker config: %w", err)
	}
	log.Printf("Installed docker credentials for %d registries", len(cfg.Auths))
	return nil
}

func Run(swapProportion float32, serverAddress string, concurrency int, token string, report Reporter) error {
	// Provide a no-op reporter if none is supplied to avoid nil deref
	if report == nil {
		report = func(int, string) {}
	}

	err := checkScratch()
	if err == nil {
		report(20, "scratch: ok")
		err = checkDocker()
	}
	if err == nil {
		report(50, "docker: ok")
		err = disableAptDaily()
	}
	if err == nil {
		report(60, "apt-daily: disabled")
		if swapProportion > 0 {
			err = checkSwap(swapProportion)
		}
	}
	if err == nil {
		report(80, "swap: ok")
		err = checkService(swapProportion, serverAddress, concurrency, token)
	}
	if err == nil {
		report(100, "service: ok")
	}

	return err
}
