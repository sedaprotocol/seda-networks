package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

var (
	/* =================================================== */
	/*                  Change as needed                   */
	/* =================================================== */
	CHAIN_ID    = "seda-1-dryrun"
	WORKING_DIR = "./mainnet"
	BINARY_URL  = "https://github.com/sedaprotocol/seda-chain/releases/download/v0.1.0-rc0/sedad-amd64"

	/* =================================================== */
	/*         The followings should rarely change         */
	/* =================================================== */
	BINARY             = "sedad"
	BINARY_PATH        = "./" + BINARY
	GENTX_DIR          = filepath.Join(WORKING_DIR, "gentx")
	SEDA_HOME          = filepath.Join(os.Getenv("HOME"), ".sedad")
	PREFIX             = "seda"
	DENOM              = "aseda"
	TEST_KEY_NAME      = "test-key"
	GENESIS_ALLOCATION = "5000000000000000000000" + DENOM       // 5000 SEDA
	DEFAULT_BOND       = "5000000000000000000000" + DENOM       // 5000 SEDA
	MAXBOND            = "600000000000000000000000000000000000" // TO-DO what number to use here?
)

func main() {
	log.Println("Downloading binary...")
	err := downloadFile(BINARY_PATH, BINARY_URL)
	if err != nil {
		log.Fatal("Error downloading binary: ", err)
	}

	// make binary executable
	err = os.Chmod(BINARY_PATH, 0755)
	if err != nil {
		log.Fatal("Error making binary executable: ", err)
	}

	log.Println("Removing existing seda home directory...")
	_, err = exec.Command("rm", "-rf", SEDA_HOME).Output()
	if err != nil {
		log.Fatal("Error removing existing seda home directory: ", err)
	}

	log.Println("Setting chain id...")
	_, err = exec.Command(BINARY_PATH, "config", "set", "client", "chain-id", CHAIN_ID).Output()
	if err != nil {
		log.Fatal("Error setting chain id: ", err)
	}

	log.Println("Setting keyring backend...")
	_, err = exec.Command(BINARY_PATH, "config", "set", "client", "keyring-backend", "test").Output()
	if err != nil {
		log.Fatal("Error setting keyring backend: ", err)
	}

	log.Println("Initializing node...")
	_, err = exec.Command(BINARY_PATH, "init", "node", "--default-denom", DENOM).Output()
	if err != nil {
		log.Fatal("Error initializing node: ", err)
	}

	log.Println("Removing existing genesis file...")
	err = os.Remove(SEDA_HOME + "/config/genesis.json")
	if err != nil {
		log.Fatal("Error removing existing genesis file: ", err)
	}

	// replace with pre-genesis.json
	log.Println("Replacing genesis.json...")
	err = copyFile(WORKING_DIR+"/pre-genesis.json", SEDA_HOME+"/config/genesis.json")
	if err != nil {
		log.Fatal("Error replacing default genesis.json: ", err)
	}

	// modify genesis time to be in the past
	log.Println("Modifying genesis time...")
	err = modifyGenesisTime(SEDA_HOME+"/config/genesis.json", "2024-01-01T18:00:00Z")
	if err != nil {
		log.Fatal("Error modifying genesis time: ", err)
	}

	// perform validation
	log.Println("Validation began...")
	gentxFiles, err := filepath.Glob(WORKING_DIR + "/gentx/*.json")
	if err != nil {
		log.Fatal("Error reading gentx files: ", err)
	}

	gentxDir := filepath.Join(SEDA_HOME, "config/gentx")
	if _, err := os.Stat(gentxDir); os.IsNotExist(err) {
		err := os.Mkdir(gentxDir, 0755)
		if err != nil {
			fmt.Println("Error creating directory:", err)
			return
		}
	}

	for _, file := range gentxFiles {
		gentxFile, err := os.ReadFile(file)
		if err != nil {
			log.Fatal("Error reading gentx file: ", err)
		}

		var gentx map[string]interface{}
		err = json.Unmarshal(gentxFile, &gentx)
		if err != nil {
			log.Fatal("Error unmarshalling gentx file: ", err)
		}

		body := gentx["body"].(map[string]interface{})
		messages := body["messages"].([]interface{})
		message := messages[0].(map[string]interface{})
		validatorAddress := message["validator_address"].(string)
		value := message["value"].(map[string]interface{})
		denom := value["denom"].(string)
		amount, ok := new(big.Int).SetString(value["amount"].(string), 10)
		if !ok {
			log.Fatal("Error converting bond amount to big.Int")
		}

		_, err = exec.Command(BINARY_PATH, "debug", "addr", validatorAddress).Output()
		if err != nil {
			log.Fatal("Error debugging validator address: ", err)
		}

		if denom != DENOM {
			log.Fatal("invalid denomination")
		}

		maxBond, ok := new(big.Int).SetString(MAXBOND, 10)
		if !ok {
			log.Fatal("Error converting max bonding amount to big.Int")
		}

		if amount.Cmp(maxBond) == 1 {
			log.Fatalf("Bonded stake exceeds limit: %d > %d", amount, maxBond)
		}

		// add genesis account, if it hasn't already been added
		addrBytes, err := sdk.GetFromBech32(validatorAddress, "sedavaloper")
		if err != nil {
			log.Fatalf("Error converting validator address to bytes: %s", err)
		}
		delegatorAddr, err := sdk.Bech32ifyAddressBytes("seda", addrBytes)
		if err != nil {
			log.Fatalf("Error converting address bytes to delegator address: %s", err)
		}

		log.Println("Adding genesis account:", delegatorAddr)
		log.Println("Amount:", GENESIS_ALLOCATION)

		var stderr bytes.Buffer
		cmd := exec.Command(BINARY_PATH, "add-genesis-account", delegatorAddr, GENESIS_ALLOCATION, "--keyring-backend", "test")
		cmd.Stderr = &stderr
		err = cmd.Run()
		if err != nil {
			if strings.Contains(stderr.String(), "cannot add account at existing address") {
				log.Printf("genesis account %s has already been added", delegatorAddr)
			} else {
				log.Fatalf("Error adding genesis account: %s", err)
			}
		}

		// // create gentx
		// _, err = exec.Command(BINARY_PATH, "gentx", keyName, DEFAULT_BOND).Output()
		// if err != nil {
		// 	log.Fatal("Error creating gentx: ", err)
		// }

		// copy the gentx file to the node directory
		err = copyFile(file, filepath.Join(gentxDir, filepath.Base(file)))
		if err != nil {
			log.Fatalf("Error copying gentx file to node directory: %s", err)
		}
	}

	log.Println("Validating finished...")

	log.Println("Collecting gentx...")
	_, err = exec.Command(BINARY_PATH, "collect-gentxs").Output()
	if err != nil {
		log.Fatal("Error collecting gentxs: ", err)
	}

	log.Println("Validating gentx...")
	_, err = exec.Command(BINARY_PATH, "validate-genesis").Output()
	if err != nil {
		log.Fatal("Error validating gentxs: ", err)
	}

	log.Println("Starting localnet...")
	cmd := exec.Command(BINARY_PATH, "start")
	err = cmd.Start()
	if err != nil {
		log.Fatal("Error starting localnet: ", err)
	}

	// wait for the chain to start
	time.Sleep(10 * time.Second)

	log.Println("Checking node status...")
	cmd = exec.Command(BINARY_PATH, "status")
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("Error checking node status: %v, output: %s", err, output)
	}

	log.Println("✅ ✅ ✅ Gentx validation passed successfully...")
}

func downloadFile(filepath string, url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	err = os.WriteFile(dst, data, 0644)
	if err != nil {
		return err
	}
	return nil
}

func modifyGenesisTime(genesisFileNamePath, timestamp string) error {
	genesisFile, err := os.ReadFile(genesisFileNamePath)
	if err != nil {
		log.Fatal(err)
	}

	var genesis map[string]interface{}
	err = json.Unmarshal(genesisFile, &genesis)
	if err != nil {
		log.Fatal(err)
	}

	genesis["genesis_time"] = timestamp
	newGenesisFile, err := json.MarshalIndent(genesis, "", "  ")
	if err != nil {
		log.Fatal(err)
	}

	err = os.WriteFile(genesisFileNamePath, newGenesisFile, 0644)

	return err
}
