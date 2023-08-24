package main

import (
	"os"
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/quaiclient"
	"github.com/spf13/viper"
)

type NodeClient struct {
	Client    *quaiclient.Client
	Connected bool
}

// Clients for RPC connection to the Prime, region, & zone ports belonging to the
// slice we are actively mining
type SliceClients struct {
	PrimeClient   NodeClient
	RegionClients []NodeClient
	ZoneClients   [][]NodeClient
}

func NewSliceClients() SliceClients {
	var sliceClients SliceClients
	sliceClients.RegionClients = make([]NodeClient, common.HierarchyDepth)
	sliceClients.ZoneClients = make([][]NodeClient, common.HierarchyDepth)
	for i := 0; i < common.HierarchyDepth; i++ {
		sliceClients.ZoneClients[i] = make([]NodeClient, common.HierarchyDepth)
	}
	return sliceClients
}

func (sc *SliceClients) allNodesConnected() bool {
	return sc.PrimeClient.Connected &&
		sc.RegionClients[0].Connected && sc.RegionClients[1].Connected && sc.RegionClients[2].Connected &&
		sc.ZoneClients[0][0].Connected && sc.ZoneClients[0][1].Connected && sc.ZoneClients[0][2].Connected &&
		sc.ZoneClients[1][0].Connected && sc.ZoneClients[1][1].Connected && sc.ZoneClients[1][2].Connected &&
		sc.ZoneClients[2][0].Connected && sc.ZoneClients[2][1].Connected && sc.ZoneClients[2][2].Connected
}

var (
	GenesisHash = common.HexToHash("0x4c35b1216decc6aa2431fa2d2a1c68f15d70f83a309041ed1bfef5ad6592a3d4")
)

func printBadHashes(primeHash common.Hash, regionHashes []common.Hash, zoneHashes [][]common.Hash) {
	fmt.Printf("HeirarchyBadHashes{\n")
	fmt.Printf("\tPrimeContext: common.HexToHash(\"%s\"),\n", primeHash)
	fmt.Printf("\tRegionContext: []common.Hash{\n")
	for _, regionHash := range regionHashes {
		fmt.Printf("\t\tcommon.HexToHash(\"%s\"),\n", regionHash)
	}
	fmt.Printf("\t},\n")
	fmt.Printf("\tZoneContext: [][]common.Hash{\n")
	for _, zoneHashSlice := range zoneHashes {
		fmt.Printf("\t\t[]common.Hash{\n")
		for _, zoneHash := range zoneHashSlice {
			fmt.Printf("\t\t\tcommon.HexToHash(\"%s\"),\n", zoneHash)
		}
		fmt.Printf("\t\t},\n")
	}
	fmt.Printf("\t},\n")
	fmt.Printf("}\n")
}

func main() {
	config, err := LoadConfig("")
	if err != nil {
		log.Fatal("cannot load config:", err)
	}
	sliceClients := connectToSlice(config)

	primeBadHash := common.HexToHash(os.Args[1])
	primeBadHeader := sliceClients.PrimeClient.Client.HeaderByHash(context.Background(), primeBadHash)
	primeTermini := GenerateTermini(sliceClients, 0, 0, primeBadHeader.ParentHash())
	regionTermini := make([][]common.Hash, common.HierarchyDepth)
	for i := 0; i < common.HierarchyDepth; i++ {
		regionTermini[i] = GenerateTermini(sliceClients, 1, i, primeTermini[i])
	}

	regionBadHashes := make([]common.Hash, common.HierarchyDepth)
	for i := 0; i < common.HierarchyDepth; i++ {
		regionTerminiHeader := sliceClients.RegionClients[i].Client.HeaderByHash(context.Background(), primeTermini[i])
		regionBadHashes[i] = sliceClients.RegionClients[i].Client.HeaderByNumber(context.Background(), "0x"+strings.ToUpper(fmt.Sprintf("%x", regionTerminiHeader.NumberU64(common.REGION_CTX)+1))).Hash()
	}

	zoneBadHashes := make([][]common.Hash, common.HierarchyDepth)
	for i := 0; i < common.HierarchyDepth; i++ {
		zoneBadHashes[i] = make([]common.Hash, common.HierarchyDepth)
	}
	for i := 0; i < common.HierarchyDepth; i++ {
		for j := 0; j < common.HierarchyDepth; j++ {
			zoneTerminiHeader := sliceClients.ZoneClients[i][j].Client.HeaderByHash(context.Background(), regionTermini[i][j])
			zoneBadHashes[i][j] = sliceClients.ZoneClients[i][j].Client.HeaderByNumber(context.Background(), "0x"+strings.ToUpper(fmt.Sprintf("%x", zoneTerminiHeader.NumberU64(common.ZONE_CTX)+1))).Hash()
		}
	}
	// go forward and collect the next block
	printBadHashes(primeBadHash, regionBadHashes, zoneBadHashes)

	return
}

func GenerateTermini(sc SliceClients, ctx, index int, hash common.Hash) []common.Hash {
	termini := make([]common.Hash, 4)

	var client *quaiclient.Client
	if ctx == common.PRIME_CTX {
		client = sc.PrimeClient.Client
	} else if ctx == common.REGION_CTX {
		client = sc.RegionClients[index].Client
	}

	isTerminiFull := func(termini []common.Hash) bool {
		var count int
		for _, t := range termini {
			if (t != common.Hash{}) {
				count++
			}
		}
		return count == 4
	}

	// This header hash serves as the Terminus value
	termini[3] = hash
	if ctx == common.PRIME_CTX || ctx == common.REGION_CTX {
		header := client.HeaderByHash(context.Background(), hash)
		termini[header.Location()[ctx]] = hash
		parent := header
		for {
			if isTerminiFull(termini) {
				break
			}
			if parent.Hash() == GenesisHash {
				for i, t := range termini {
					if (t == common.Hash{}) {
						termini[i] = GenesisHash
					}
				}
				break
			}
			header := client.HeaderByHash(context.Background(), parent.ParentHash(ctx))
			if (termini[header.Location()[ctx]] == common.Hash{}) {
				termini[header.Location()[ctx]] = header.Hash()
			}
			parent = header
		}
	}
	return termini
}

// connectToSlice takes in a config and retrieves the Prime, Region, and Zone client
// that is used for mining in a slice.
func connectToSlice(config Config) SliceClients {
	var err error
	clients := NewSliceClients()

	for {
		if clients.allNodesConnected() {
			break
		}
		clients.PrimeClient.Client, err = quaiclient.Dial(config.PrimeURL)
		if err != nil {
			log.Println("Unable to connect to node:", "Prime", config.PrimeURL)
		} else {
			clients.PrimeClient.Connected = true
		}
		for i := 0; i < common.HierarchyDepth; i++ {
			clients.RegionClients[i].Client, err = quaiclient.Dial(config.RegionURLs[i])
			if err != nil {
				log.Println("Unable to connect to node:", "Region", config.RegionURLs[i])
			} else {
				clients.RegionClients[i].Connected = true
			}
			for j := 0; j < common.HierarchyDepth; j++ {
				clients.ZoneClients[i][j].Client, err = quaiclient.Dial(config.ZoneURLs[i][j])
				if err != nil {
					log.Println("Unable to connect to node:", "Zone", config.ZoneURLs[i][j])
				} else {
					clients.ZoneClients[i][j].Connected = true
				}
			}
		}
	}
	return clients
}

// Config holds the configuration parameters for quai-manager
type Config struct {
	PrimeURL   string
	RegionURLs []string
	ZoneURLs   [][]string
}

// LoadConfig reads configuration from file or environment variables.
func LoadConfig(path string) (config Config, err error) {
	viper.AddConfigPath("./config")
	viper.SetConfigName("config") // name of config file (without extension)
	viper.SetConfigType("yaml")   // REQUIRED if the config file does not have the extension in the name
	viper.AddConfigPath(".")      // optionally look for config in the working directory
	err = viper.ReadInConfig()    // Find and read the config file

	if err != nil { // Handle errors reading the config file
		panic(fmt.Errorf("Fatal error config file: %w \n", err))
	}

	err = viper.Unmarshal(&config)
	return
}
