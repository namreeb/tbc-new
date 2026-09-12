package core

import (
	"fmt"
	"sync"
	"testing"

	"github.com/wowsims/tbc/sim/core/proto"
)

// Concurrent requests register the items they carry while other requests are
// reading the database. Before the maps were guarded by a read-write lock this
// aborted the whole server process with "concurrent map read and map write",
// typically partway through a large batch sim. Run with -race to catch any
// unguarded access that reappears.
func TestDatabaseConcurrentReadWrite(t *testing.T) {
	const writers = 8
	const readers = 8
	const perWriter = 200
	const baseID = int32(900_000_000)

	var wg sync.WaitGroup
	start := make(chan struct{})

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < perWriter; i++ {
				id := baseID + int32(w*perWriter+i)
				addToDatabase(&proto.SimDatabase{
					Items:       []*proto.SimItem{{Id: id, Name: fmt.Sprintf("Race Item %d", id), ScalingOptions: map[int32]*proto.ScalingItemProperties{0: {}}}},
					Gems:        []*proto.SimGem{{Id: id, Name: "Race Gem"}},
					Enchants:    []*proto.SimEnchant{{EffectId: id, Name: "Race Enchant"}},
					Consumables: []*proto.Consumable{{Id: id, Name: "Race Consumable"}},
				})
			}
		}(w)
	}

	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			<-start
			for i := 0; i < writers*perWriter; i++ {
				id := baseID + int32(i)
				GetItemByID(id)
				LookupGem(id)
				GetEnchantByEffectID(id)
				GetConsumableByID(id)
				GetSpellEffectByID(id)
				if i%50 == 0 {
					AllItems()
				}
			}
		}(r)
	}

	close(start)
	wg.Wait()

	if _, ok := LookupItem(baseID); !ok {
		t.Fatalf("item %d should have been registered", baseID)
	}
	if got := len(AllItems()); got < writers*perWriter {
		t.Fatalf("expected at least %d items, got %d", writers*perWriter, got)
	}
}
