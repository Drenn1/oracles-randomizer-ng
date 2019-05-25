// Package rom deals with the structure of the oracles ROM files themselves.
// The given addresses are for the English versions of the games, and if two
// are specified, Ages comes first.
package randomizer

import (
	"crypto/sha1"
	"fmt"
	"math/rand"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v2"
)

const bankSize = 0x4000

var rings []string

// only applies to seasons! used for warps
var dungeonNameRegexp = regexp.MustCompile(`^d[1-8]$`)

func initRom(b []byte, game int) {
	Treasures = LoadTreasures(b, game)
	ItemSlots = LoadSlots(b, game)
	globalRomBanks = initRomBanks(game)

	for _, slot := range ItemSlots {
		slot.Treasure = Treasures[slot.treasureName]
	}

	if err := yaml.Unmarshal(
		FSMustByte(false, "/romdata/rings.yaml"), &rings); err != nil {
		panic(err)
	}
}

// Addr is a fully-specified memory address.
type Addr struct {
	bank   uint8
	offset uint16
}

// fullOffset returns the actual offset of the address in the ROM, based on
// bank number and relative address.
func (a *Addr) fullOffset() int {
	var bankOffset int
	if a.bank >= 2 {
		bankOffset = bankSize * (int(a.bank) - 1)
	}
	return bankOffset + int(a.offset)
}

func romIsAges(b []byte) bool {
	return string(b[0x134:0x13f]) == "ZELDA NAYRU"
}

func romIsSeasons(b []byte) bool {
	return string(b[0x134:0x13d]) == "ZELDA DIN"
}

func romIsNonJp(b []byte) bool {
	return b[0x014a] != 0
}

func romIsVanilla(b []byte) bool {
	knownSum := "\x88\x03\x74\xfb\x97\x8b\x18\xaf\x4a\xa5\x29\xe2\xe3\x2f\x7f" +
		"\xfb\x4d\x7d\xd2\xf4"
	if romIsSeasons(b) {
		knownSum = "\xba\x12\x68\x29\x0f\xb2\xb1\xb7\x05\x05\xd2\xd7\xb5\x82" +
			"\x5f\xc8\xa4\x81\x6a\x4b"
	}
	sum := sha1.Sum(b)

	return string(sum[:]) == knownSum
}

// get mutables in order, so that sums are consistent with the same seed
func orderedKeys(m map[string]Mutable) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// mutateRom changes the contents of loaded ROM bytes in place. It returns a
// checksum of the result or an error.
func mutateRom(b []byte, game int, warpMap map[string]string,
	dungeons bool) ([]byte, error) {
	// need to set this *before* treasure map data
	if len(warpMap) != 0 {
		setWarps(b, game, warpMap, dungeons)
	}

	if game == gameSeasons {
		northHoronSeason :=
			codeMutables["northHoronSeason"].New[0]
		codeMutables["initialSeason"].New =
			[]byte{0x2d, northHoronSeason}
		westernCoastSeason :=
			codeMutables["westernCoastSeason"].New[0]
		codeMutables["seasonAfterPirateCutscene"].New =
			[]byte{westernCoastSeason}

		setTreasureMapData()

		// explicitly set these addresses and IDs after their functions
		codeAddr := codeMutables["setStarOreIds"].Addrs[0]
		ItemSlots["subrosia seaside"].idAddrs[0].offset = codeAddr.offset + 2
		ItemSlots["subrosia seaside"].subIDAddrs[0].offset = codeAddr.offset + 5
		codeAddr = codeMutables["setHardOreIds"].Addrs[0]
		ItemSlots["great furnace"].idAddrs[0].offset = codeAddr.offset + 2
		ItemSlots["great furnace"].subIDAddrs[0].offset = codeAddr.offset + 5
		codeAddr = codeMutables["script_diverGiveItem"].Addrs[0]
		ItemSlots["master diver's reward"].idAddrs[0].offset = codeAddr.offset + 1
		ItemSlots["master diver's reward"].subIDAddrs[0].offset = codeAddr.offset + 2
		codeAddr = codeMutables["createMtCuccoItem"].Addrs[0]
		ItemSlots["mt. cucco, platform cave"].idAddrs[0].offset = codeAddr.offset + 2
		ItemSlots["mt. cucco, platform cave"].subIDAddrs[0].offset = codeAddr.offset + 1
	} else {
		// explicitly set these addresses and IDs after their functions
		mut := codeMutables["script_soldierGiveItem"]
		slot := ItemSlots["deku forest soldier"]
		slot.idAddrs[0].offset = mut.Addrs[0].offset + 13
		slot.subIDAddrs[0].offset = mut.Addrs[0].offset + 14
		mut = codeMutables["script_giveTargetCartsSecondPrize"]
		codeAddr := mut.Addrs[0]
		ItemSlots["target carts 2"].idAddrs[1].offset = codeAddr.offset + 1
		ItemSlots["target carts 2"].subIDAddrs[1].offset = codeAddr.offset + 2
	}

	setBossItemAddrs()
	setSeedData(game)
	setRoomTreasureData(game)

	// regenerate collect mode table to accommodate changes based on contents.
	codeMutables["collectModeTable"].New = []byte(makeCollectModeTable())

	// set the text IDs for all rings to $ff (blank), since custom code deals
	// with text
	for _, treasure := range Treasures {
		if treasure.id == 0x2d {
			treasure.text = 0xff
		}
	}

	var err error
	mutables := getAllMutables()
	for _, k := range orderedKeys(mutables) {
		err = mutables[k].Mutate(b)
		if err != nil {
			return nil, err
		}
	}

	// explicitly set these items after their functions are written
	writeBossItems(b)
	if game == gameSeasons {
		ItemSlots["subrosia seaside"].Mutate(b)
		ItemSlots["great furnace"].Mutate(b)
		ItemSlots["master diver's reward"].Mutate(b)

		// annoying special case to prevent text on key drop
		mut := ItemSlots["d7 armos puzzle"]
		if mut.Treasure.id == Treasures["d7 small key"].id {
			b[mut.subIDAddrs[0].fullOffset()] = 0x01
		}
	} else {
		ItemSlots["nayru's house"].Mutate(b)
		ItemSlots["deku forest soldier"].Mutate(b)
		ItemSlots["target carts 2"].Mutate(b)
		ItemSlots["hidden tokay cave"].Mutate(b)

		// other special case to prevent text on key drop
		mut := ItemSlots["d8 stalfos"]
		if mut.Treasure.id == Treasures["d8 small key"].id {
			b[mut.subIDAddrs[0].fullOffset()] = 0x00
		}
	}

	setCompassData(b, game)
	setLinkedData(b, game)
	applyText(b, gameNames[game])

	outSum := sha1.Sum(b)
	return outSum[:], nil
}

// verifyRom checks all the package's data against the ROM to see if it
// matches. It returns a slice of errors describing each mismatch.
func verifyRom(b []byte, game int) []error {
	errors := make([]error, 0)
	for k, m := range getAllMutables() {
		// ignore special cases that would error even when correct
		switch k {
		// seasons shop items
		case "zero shop text", "member's card", "treasure map",
			"rare peach stone", "ribbon":
		// seasons flutes
		case "dimitri's flute", "moosh's flute":
		// seasons linked chests
		case "spool swamp cave", "woods of winter, 2nd cave",
			"dry eyeglass lake, west cave":
		// seasons misc.
		case "temple of seasons", "blaino prize", "mt. cucco, platform cave",
			"diving spot outside D4":
		// ages progressive w/ different item IDs
		case "nayru's house", "tokkey's composition", "rescue nayru",
			"d6 present vire chest":
		// ages misc.
		case "south shore dirt", "target carts 2", "sea of storms past",
			"starting chest", "graveyard poe":
		default:
			if err := m.Check(b); err != nil {
				errors = append(errors, fmt.Errorf("%s: %v", k, err))
			}
		}
	}

	if len(errors) > 0 {
		return errors
	}
	return nil
}

// set the initial satchel and slingshot seeds (and selections) based on what
// grows on the horon village tree, and set the map icon for each tree to match
// the seed type.
func setSeedData(game int) {
	var seedType byte
	if game == gameSeasons {
		seedType = ItemSlots["horon village tree"].Treasure.id
	} else {
		seedType = ItemSlots["south lynna tree"].Treasure.id
	}

	if game == gameSeasons {
		// satchel/slingshot starting seeds
		codeMutables["satchelInitialSeeds"].New[0] = 0x20 + seedType
		codeMutables["editGainLoseItemsTables"].New[1] = 0x20 + seedType

		for _, name := range []string{
			"satchelInitialSelection", "slingshotInitialSelection"} {
			codeMutables[name].New[1] = seedType
		}

		for _, names := range [][]string{
			{"horon village tree", "horonVillageTreeMapIcon"},
			{"north horon tree", "northHoronTreeMapIcon"},
			{"woods of winter tree", "woodsOfWinterTreeMapIcon"},
			{"spool swamp tree", "spoolSwampTreeMapIcon"},
			{"sunken city tree", "sunkenCityTreeMapIcon"},
			{"tarm ruins tree", "tarmRuinsTreeMapIcon"},
		} {
			id := ItemSlots[names[0]].Treasure.id
			codeMutables[names[1]].New[0] = 0x15 + id
		}
	} else {
		// set high nybbles (seed types) of seed tree interactions
		setTreeNybble(codeMutables["symmetryCityTreeSubId"],
			ItemSlots["symmetry city tree"])
		setTreeNybble(codeMutables["southLynnaPresentTreeSubId"],
			ItemSlots["south lynna tree"])
		setTreeNybble(codeMutables["crescentIslandTreeSubId"],
			ItemSlots["crescent island tree"])
		setTreeNybble(codeMutables["zoraVillagePresentTreeSubId"],
			ItemSlots["zora village tree"])
		setTreeNybble(codeMutables["rollingRidgeWestTreeSubId"],
			ItemSlots["rolling ridge west tree"])
		setTreeNybble(codeMutables["ambisPalaceTreeSubId"],
			ItemSlots["ambi's palace tree"])
		setTreeNybble(codeMutables["rollingRidgeEastTreeSubId"],
			ItemSlots["rolling ridge east tree"])
		setTreeNybble(codeMutables["southLynnaPastTreeSubId"],
			ItemSlots["south lynna tree"])
		setTreeNybble(codeMutables["dekuForestTreeSubId"],
			ItemSlots["deku forest tree"])
		setTreeNybble(codeMutables["zoraVillagePastTreeSubId"],
			ItemSlots["zora village tree"])

		// satchel and shooter come with south lynna tree seeds
		codeMutables["satchelInitialSeeds"].New[0] = 0x20 + seedType
		codeMutables["seedShooterGiveSeeds"].New[6] = 0x20 + seedType
		for _, name := range []string{"satchelInitialSelection",
			"shooterInitialSelection"} {
			codeMutables[name].New[1] = seedType
		}

		// set map icons
		for _, name := range []string{"crescent island tree",
			"symmetry city tree", "south lynna tree", "zora village tree",
			"rolling ridge west tree", "ambi's palace tree",
			"rolling ridge east tree", "deku forest tree"} {
			codeName := inflictCamelCase(name) + "MapIcon"
			if name == "south lynna tree" || name == "zora village tree" {
				for _, n := range []string{"1", "2"} {
					codeMutables[codeName+n].New[0] =
						0x15 + ItemSlots[name].Treasure.id
				}
			} else {
				codeMutables[codeName].New[0] =
					0x15 + ItemSlots[name].Treasure.id
			}
		}
	}
}

// converts e.g. "hello world" to "helloWorld". disgusting tbh
func inflictCamelCase(s string) string {
	return fmt.Sprintf("%c%s", s[0], strings.ReplaceAll(
		strings.Title(strings.ReplaceAll(s, "'", "")), " ", "")[1:])
}

// fill table. initial table is blank, since it's created before items are
// placed.
func setRoomTreasureData(game int) {
	codeMutables["roomTreasures"].New = []byte(makeRoomTreasureTable(game))

	if game == gameSeasons {
		t := ItemSlots["d7 zol button"].Treasure
		codeMutables["aboveD7ZolButtonId"].New = []byte{t.id}
		codeMutables["aboveD7ZolButtonSubid"].New = []byte{t.subID}
	}
}

// sets the high nybble (seed type) of a seed tree interaction in ages.
func setTreeNybble(subid *MutableRange, slot *MutableSlot) {
	subid.New[0] = (subid.New[0] & 0x0f) | (slot.Treasure.id << 4)
}

// set the locations of the sparkles for the jewels on the treasure map.
func setTreasureMapData() {
	for _, name := range []string{"round", "pyramid", "square", "x-shaped"} {
		label := strings.ReplaceAll(name, "-s", "S") + "JewelCoords" // lol
		slot := lookupItemSlot(name + " jewel")
		codeMutables[label].New[0] = slot.mapCoords
	}
}

// set dungeon properties so that the compass beeps in the rooms actually
// containing small keys and boss keys.
func setCompassData(b []byte, game int) {
	var prefixes []string
	if game == gameSeasons {
		prefixes = []string{"d0", "d1", "d2", "d3", "d4", "d5", "d6", "d7",
			"d8"}
	} else {
		prefixes = []string{"d0", "d1", "d2", "d3", "d4", "d5", "d6 present",
			"d6 past", "d7", "d8"}
	}

	// clear key flags
	for _, prefix := range prefixes {
		for name, slot := range ItemSlots {
			if strings.HasPrefix(name, prefix+" ") {
				offset := getDungeonPropertiesAddr(
					game, slot.group, slot.room).fullOffset()
				b[offset] = b[offset] & 0xed // reset bit 4
			}
		}
	}

	// set key flags
	for _, prefix := range prefixes {
		slots := lookupAllItemSlots(fmt.Sprintf("%s small key", prefix))
		switch prefix {
		case "d0", "d6 present":
			break
		case "d6 past":
			slots = append(slots, lookupItemSlot("d6 boss key"))
		default:
			slots = append(slots,
				lookupItemSlot(fmt.Sprintf("%s boss key", prefix)))
		}

		for _, slot := range slots {
			offset := getDungeonPropertiesAddr(
				game, slot.group, slot.room).fullOffset()
			b[offset] = (b[offset] & 0xbf) | 0x10 // set bit 4, reset bit 6
		}
	}
}

// returns the slot where the named item was placed. this only works for unique
// items, of course.
func lookupItemSlot(itemName string) *MutableSlot {
	if slots := lookupAllItemSlots(itemName); len(slots) > 0 {
		return slots[0]
	} else {
		return nil
	}
}

// returns all slots where the named item was placed.
func lookupAllItemSlots(itemName string) []*MutableSlot {
	t := Treasures[itemName]
	slots := make([]*MutableSlot, 0)
	for _, slot := range ItemSlots {
		if slot.Treasure == t {
			slots = append(slots, slot)
		}
	}
	return slots
}

// get the location of the dungeon properties byte for a specific room.
func getDungeonPropertiesAddr(game int, group, room byte) *Addr {
	offset := uint16(room)
	if game == gameSeasons {
		offset += 0x4d41
	} else {
		offset += 0x4dce
	}
	if group%2 != 0 {
		offset += 0x100
	}
	return &Addr{0x01, offset}
}

// randomizeRingPool randomizes the types of rings in the item pool, returning
// a map of vanilla ring names to the randomized ones.
func randomizeRingPool(src *rand.Rand, game int,
	planValues []string) (map[string]string, error) {
	nameMap := make(map[string]string)
	usedRings := make([]bool, 0x40)

	keys := make([]string, len(ItemSlots))
	i := 0
	for key, _ := range ItemSlots {
		keys[i] = key
		i++
	}
	sort.Strings(keys)

	nRings := 0
	for _, slot := range ItemSlots {
		if slot.Treasure.id == 0x2d {
			nRings++
		}
	}
	ringValues, i := make([]int, nRings), 0

	// load planned values first
	for _, v := range planValues {
		for id, name := range rings {
			if v == name {
				if i >= len(ringValues) {
					return nil, fmt.Errorf("too many rings in plan")
				}
				ringValues[i] = id
				i++
				break
			}
		}
	}

	// then roll random ones for the rest
	for i < len(ringValues) {
		// loop until we get a ring that's not literally useless, and which
		// we haven't used before.
		done := false
		for !done {
			param := src.Intn(0x40)
			switch rings[param] {
			case "friendship ring", "GBA time ring", "GBA nature ring",
				"slayer's ring", "rupee ring", "victory ring", "sign ring",
				"100th ring":
				break
			case "rang ring L-1", "rang ring L-2", "green joy ring":
				// these rings are literally useless in ages.
				if game == gameAges {
					break
				}
				fallthrough
			default:
				if !usedRings[param] {
					usedRings[param] = true
					ringValues[i] = param
					done = true
					i++
				}
			}
		}
	}
	sort.Ints(ringValues)

	i = 0
	for _, key := range keys {
		slot := ItemSlots[key]
		if slot.Treasure.id == 0x2d {
			oldName := findTreasureName(slot.Treasure)
			slot.Treasure.param = byte(ringValues[i])
			slot.Treasure.displayName = rings[ringValues[i]]
			nameMap[oldName] = slot.Treasure.displayName
			i++
		}
	}

	return nameMap, nil
}

func setBossItemAddrs() {
	table := codeMutables["bossItemTable"]

	for i := uint16(1); i <= 8; i++ {
		slot := ItemSlots[fmt.Sprintf("d%d boss", i)]
		slot.idAddrs[0].offset = table.Addrs[0].offset + i*2
		slot.subIDAddrs[0].offset = table.Addrs[0].offset + i*2 + 1
	}
}

func writeBossItems(b []byte) {
	for i := 1; i <= 8; i++ {
		ItemSlots[fmt.Sprintf("d%d boss", i)].Mutate(b)
	}
}

// set data to make linked playthroughs isomorphic to unlinked ones.
func setLinkedData(b []byte, game int) {
	if game == gameSeasons {
		// set linked starting / hero's cave terrace items based on which items
		// in unlinked hero's cave aren't keys. order matters.
		var tStart, tCave *Treasure
		if ItemSlots["d0 key chest"].Treasure.id == 0x30 {
			tStart = ItemSlots["d0 sword chest"].Treasure
			tCave = ItemSlots["d0 rupee chest"].Treasure
		} else {
			tStart = ItemSlots["d0 key chest"].Treasure
			tCave = ItemSlots["d0 sword chest"].Treasure
		}

		// give this item at start
		linkedStartItem := &MutableSlot{
			idAddrs:    []Addr{{0x0a, 0x7ffd}},
			subIDAddrs: []Addr{{0x0a, 0x7ffe}},
			Treasure:   tStart,
		}
		linkedStartItem.Mutate(b)

		// create slot for linked hero's cave terrace
		linkedChest := &MutableSlot{
			treasureName: "rupees, 20",
			idAddrs:      []Addr{{0x15, 0x50e2}},
			subIDAddrs:   []Addr{{0x15, 0x50e3}},
			group:        0x05,
			room:         0x2c,
			collectMode:  collectModes["chest"],
			mapCoords:    0xd4,
		}
		linkedChest.Treasure = tCave
		linkedChest.Mutate(b)
	}
}

// -- dungeon entrance / subrosia portal connections --

type WarpData struct {
	// loaded from yaml
	Entry, Exit uint16
	MapTile     byte

	// set after loading
	bank, vanillaMapTile         byte
	len, entryOffset, exitOffset int

	vanillaEntryData, vanillaExitData []byte // read from rom
}

func setWarps(b []byte, game int, warpMap map[string]string, dungeons bool) {
	// load yaml data
	wd := make(map[string](map[string]*WarpData))
	if err := yaml.Unmarshal(
		FSMustByte(false, "/romdata/warps.yaml"), wd); err != nil {
		panic(err)
	}
	var warps map[string]*WarpData
	if game == gameSeasons {
		warps = wd["seasons"]
	} else {
		warps = wd["ages"]
	}

	// read vanilla data
	for name, warp := range warps {
		if strings.HasSuffix(name, "essence") {
			warp.len = 4
			if game == gameSeasons {
				warp.bank = 0x09
			} else {
				warp.bank = 0x0a
			}
		} else {
			warp.bank, warp.len = 0x04, 2
		}
		warp.entryOffset = (&Addr{warp.bank, warp.Entry}).fullOffset()
		warp.vanillaEntryData = make([]byte, warp.len)
		copy(warp.vanillaEntryData,
			b[warp.entryOffset:warp.entryOffset+warp.len])
		warp.exitOffset = (&Addr{warp.bank, warp.Exit}).fullOffset()
		warp.vanillaExitData = make([]byte, warp.len)
		copy(warp.vanillaExitData,
			b[warp.exitOffset:warp.exitOffset+warp.len])

		warp.vanillaMapTile = warp.MapTile
	}

	// ages needs essence warp data to d6 present entrance, even though it
	// doesn't exist in vanilla.
	if game == gameAges {
		warps["d6 present essence"] = &WarpData{
			vanillaExitData: []byte{0x81, 0x0e, 0x16, 0x01},
		}
	}

	// set randomized data
	for srcName, destName := range warpMap {
		src, dest := warps[srcName], warps[destName]
		for i := 0; i < src.len; i++ {
			b[src.entryOffset+i] = dest.vanillaEntryData[i]
			b[dest.exitOffset+i] = src.vanillaExitData[i]
		}
		dest.MapTile = src.vanillaMapTile

		destEssence := warps[destName+" essence"]
		if destEssence != nil && destEssence.exitOffset != 0 {
			srcEssence := warps[srcName+" essence"]
			for i := 0; i < destEssence.len; i++ {
				b[destEssence.exitOffset+i] = srcEssence.vanillaExitData[i]
			}
		}
	}

	if game == gameSeasons {
		// set treasure map data. because of d8, portals go first, then dungeon
		// entrances.
		conditions := [](func(string) bool){
			dungeonNameRegexp.MatchString,
			func(s string) bool { return strings.HasSuffix(s, "portal") },
		}
		for _, cond := range conditions {
			changeTreasureMapTiles(func(c chan byteChange) {
				for name, warp := range warps {
					if cond(name) {
						c <- byteChange{warp.vanillaMapTile, warp.MapTile}
					}
				}
				close(c)
			})
		}

		if dungeons {
			// remove alternate d2 entrances and connect d2 stairs exits
			// directly to each other
			src, dest := warps["d2 alt left"], warps["d2 alt right"]
			b[src.exitOffset] = dest.vanillaEntryData[0]
			b[src.exitOffset+1] = dest.vanillaEntryData[1]
			b[dest.exitOffset] = src.vanillaEntryData[0]
			b[dest.exitOffset+1] = src.vanillaEntryData[1]

			// also enable removal of the stair tiles
			mut := codeMutables["d2AltEntranceTileSubs"]
			mut.New[0], mut.New[5] = 0x00, 0x00
		}
	}
}

type byteChange struct {
	old, new byte
}

// process a set of treasure map tile changes in a way that ensures each tile
// is substituted only once (per call to this function).
func changeTreasureMapTiles(generate func(chan byteChange)) {
	pendingTiles := make(map[*MutableSlot]byte)
	c := make(chan byteChange)
	go generate(c)

	for change := range c {
		for _, slot := range ItemSlots {
			// diving spot outside d4 would be mistaken for a d4 check
			if slot.mapCoords == change.old &&
				slot != ItemSlots["diving spot outside D4"] {
				pendingTiles[slot] = change.new
			}
		}
	}

	for slot, tile := range pendingTiles {
		slot.mapCoords = tile
	}
}