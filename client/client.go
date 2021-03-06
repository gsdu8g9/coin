package main

import (
	"coin"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"time"

	cpb "coin/service"

	"encoding/json"

	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

var (
	debug       = flag.Bool("d", false, "debug mode")
	tosses      = flag.Int("t", 2, "number of tosses")
	user        = flag.Int("u", 0, "the client owner user id")
	key         = flag.String("k", "", "secret key assigned")
	serverHost  = flag.String("s", "localhost", "server hostname, eg goblimey.com")
	serverPort  = flag.Int("p", -1, "server port offset from 50051.") // no default, see checkMandatoryF
	maxSleep    = flag.Int("quit", 4, "number of multiples of 5 seconds before server declared dead")
	config      = flag.String("f", "", "config file of options")
	serverAlive bool
	name        string
)

// annouceWin is what causes the server to issue a cancellation
func annouceWin(c cpb.CoinClient, nonce uint32, block []byte, winner string) bool {
	win := &cpb.Win{Block: block, Nonce: nonce, Identity: winner}
	r, err := c.Announce(context.Background(), &cpb.AnnounceRequest{Win: win})
	if skipF("could not announce win", err) {
		return false
	}
	return r.Ok
}

// getCancel makes a blocking request to the server
func getCancel(c cpb.CoinClient, name string, stopLooking chan struct{}, endLoop chan struct{}) {
	_, err := c.GetCancel(context.Background(), &cpb.GetCancelRequest{Name: name})
	skipF("could not request cancellation", err) // drop through
	stopLooking <- struct{}{}                    // stop search
	endLoop <- struct{}{}                        // quit loop
}

// dice
func toss() int {
	return rand.Intn(6)
}

// rools returns true if n tosses are all 5's
func rolls(n int) bool {
	ok := true
	for i := 0; i < n; i++ {
		ok = ok && toss() == 5
		if !ok {
			break
		}
	}
	return ok
}

// search tosses two dice waiting for a double 5. exit on cancel or win
func search(work *cpb.Work, stopLooking chan struct{}) (uint32, bool) {
	// we must combine the coinbase + rest of block here  ...
	prepare(work)
	// toy version
	var theNonce uint32
	var ok bool
	tick := time.Tick(1 * time.Second)
	for cn := 0; ; cn++ {
		if rolls(*tosses) { // a win?
			// if cn == 6 { // debug - all fire at once
			theNonce = uint32(cn)
			debugF("winning! nonce: %d\n", cn)
			ok = true
			break
		}
		// check for a stop order
		select {
		case <-stopLooking: // if so ... break out of this cycle, ok=false
			return theNonce, false
		default: // continue
		}
		// wait for a second here ...
		<-tick
		debugF("| %d\n", cn)
	}
	return theNonce, ok
}

var (
	coinbase      coin.Transaction
	block         coin.Block
	target, share []byte
)

func prepare(work *cpb.Work) { //{Coinbase: coinbaseBytes, Block: partblock, Skel: merkSkel}
	//work.Coinbase
	coinbase = coin.Transaction(work.Coinbase)
	block = coin.Block(work.Block)
	cbhash := coin.Sha256(coinbase)                        // TODO - confirm this is not a double hash!!
	merkleroot, err := coin.Skel2Merkle(cbhash, work.Skel) // TODO - remove error here ...
	if err != nil {
		log.Fatal("failed to create merkelroot")
	}
	block.AddMerkle(merkleroot)
	target = coin.Bits2Target(work.Bits)
	/*
		this routine should place the coinbase in the blockheader
		compute the new merkle root hash and put in place
		change these when called to - each cycle? every overflow?

	*/
}

// genName takes userid and key to generate
// a login and time
func genName(user uint32, key string) (string, string, error) {
	time := fmt.Sprintf("%x", uint32(time.Now().Unix()))
	login, err := coin.GenLogin(user, key, time)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	return login, time, nil //
}

type jsonConfig struct {
	User   int
	Key    string
	Server string
	Port   int
}

func readConfig(filename string) {
	jsondata, err := ioutil.ReadFile("./" + filename)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	var config jsonConfig
	if err := json.Unmarshal(jsondata, &config); err != nil {
		log.Fatalf("error: %v", err)
	}
	// fmt.Printf("%+v", config) // debug
	// check these are set?
	*user = config.User
	*serverHost = config.Server
	*serverPort = config.Port
	*key = config.Key
}

func main() {
	rand.Seed(time.Now().UTC().UnixNano())
	flag.Parse()
	if *config != "" {
		readConfig(*config)
	}
	checkMandatoryF() // ensure enough config data
	address := fmt.Sprintf("%s:%d", *serverHost, 50051+*serverPort)
	debugF("connecting to server %s", address)
	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	defer conn.Close()

	c := cpb.NewCoinClient(conn)
	serverAlive = true
	countdown := 0
	// outer OMIT
	for {
		userID := uint32(*user)
		n, t, err := genName(userID, *key) // use time as well as these two
		if err != nil {
			log.Fatalf("getname error %v\n", err)
		}
		log.Printf("name: %s,time: %s\n", n, t)
		name = n
		r, err := c.Login(context.Background(), &cpb.LoginRequest{Name: name, User: userID, Time: t}) // HL
		if skipF("could not login", err) {                                                            // HL
			time.Sleep(5 * time.Second)
			countdown++
			if countdown > *maxSleep {
				log.Fatalf("%s\n", "Server dead ... quitting") // HL
			}
			continue
		} else if !serverAlive { // HL
			countdown = 0
			serverAlive = true // we are back
		}
		log.Printf("Login successful. Assigned id: %d\n", r.Id)
		// main cycle OMIT
		for {
			var ( // OMIT
				work                 *cpb.Work     // OMIT
				stopLooking, endLoop chan struct{} // OMIT
				theNonce             uint32        // OMIT
				ok                   bool          // OMIT
			) // OMIT
			fmt.Printf("Fetching work %s ..\n", name)
			r, err := c.GetWork(context.Background(), &cpb.GetWorkRequest{Name: name})
			if skipF("could not get work", err) {
				break
			}
			work = r.Work                        // HL
			stopLooking = make(chan struct{}, 1) // HL
			endLoop = make(chan struct{}, 1)     // HL
			// look out for  cancellation
			go getCancel(c, name, stopLooking, endLoop) // HL
			// search blocks
			theNonce, ok = search(work, stopLooking) // HL
			if ok {                                  // we completed search
				fmt.Printf("%s ... sending solution (%d) \n", name, theNonce)
				win := annouceWin(c, theNonce, work.Block, name) // HL
				if win {                                         // late?
					fmt.Printf("== %s == FOUND -> %d\n", name, theNonce)
				}
			}
			<-endLoop // wait here for cancel from server
			fmt.Printf("-----------------------\n")
		}
		// main end OMIT
	}
} // outerend OMIT

// utilities

func checkMandatoryF() {
	if *user == 0 {
		log.Fatalf("%s\n", "Client must have identity. Use -u switch")
	}
	if *serverPort == -1 {
		log.Fatalf("%s\n", "Client must give server port. Use -p switch")
	}
	if *key == "" {
		log.Fatalf("%s\n", "Client must have secret. Use -k switch")
	}
}

func skipF(message string, err error) bool {
	if err != nil {
		if *debug {
			log.Printf(message+": %v", err)
		} else {
			log.Println(message)
		}
		if serverAlive {
			serverAlive = false
		}
		return true // we have skipped
	}
	return false
}

func debugF(format string, args ...interface{}) {
	if *debug {
		log.Printf(format, args...)
	}
}
