package cmd

import (
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"
	"github.com/wowsims/tbc/sim/core"
	"github.com/wowsims/tbc/sim/core/proto"
	"google.golang.org/protobuf/encoding/protojson"
)

var (
	bulkInfile  string
	bulkOutfile string
	bulkVerbose bool
)

var bulkCmd = &cobra.Command{
	Use:   "bulk",
	Short: "run a batch (bulk) sim from a request exported by the web UI",
	Long: `Runs a batch sim: enumerates gear combinations from the request's item pool,
gems each one, drops those failing a stat constraint, sims the rest and prints
the top results. The input is the JSON the web UI's batch tab exports
("Export batch JSON"), a BulkSimRequest in protojson format.`,
	Run: bulkMain,
}

func init() {
	bulkCmd.Flags().StringVar(&bulkInfile, "infile", "batch.json", "location of input file (BulkSimRequest in protojson format)")
	bulkCmd.Flags().StringVar(&bulkOutfile, "outfile", "", "location of output file, defaults to stdout")
	bulkCmd.Flags().BoolVar(&bulkVerbose, "verbose", false, "print progress during runtime")
	bulkCmd.MarkFlagRequired("infile")
}

func bulkMain(cmd *cobra.Command, args []string) {
	data, err := os.ReadFile(bulkInfile)
	if err != nil {
		log.Fatalf("failed to load input json file %q: %v", bulkInfile, err)
	}
	request := &proto.BulkSimRequest{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, request); err != nil {
		log.Fatalf("failed to parse input json file: %s", err)
	}

	if count := core.BulkSimCount(request); count.ErrorResult != "" {
		log.Fatalf("invalid batch request: %s", count.ErrorResult)
	} else if bulkVerbose {
		fmt.Fprintf(os.Stderr, "%d combinations\n", count.Combinations)
	}

	progress := make(chan *proto.ProgressMetrics, 100)
	core.BulkSimAsync(request, progress, "cli-bulk-sim")

	var result *proto.BulkSimResult
	lastPhase := proto.BulkSimPhase_BulkSimPhaseUnknown
	for update := range progress {
		if update.FinalBulkResult != nil {
			result = update.FinalBulkResult
			break
		}
		if bulkVerbose && (update.BulkPhase != lastPhase || update.CompletedSims == update.TotalSims) {
			fmt.Fprintf(os.Stderr, "%s: %d / %d\n", update.BulkPhase, update.CompletedSims, update.TotalSims)
			lastPhase = update.BulkPhase
		}
	}
	if result.Error != nil {
		log.Fatalf("batch sim failed: %s", result.Error.Message)
	}

	output, err := (protojson.MarshalOptions{EmitUnpopulated: true, Multiline: true}).Marshal(result)
	if err != nil {
		log.Fatalf("failed to marshal results: %s", err)
	}
	if bulkOutfile == "" {
		fmt.Println(string(output))
		return
	}
	if err := os.WriteFile(bulkOutfile, output, 0666); err != nil {
		log.Fatalf("failed to write output file: %s", err)
	}
	if bulkVerbose {
		fmt.Fprintf(os.Stderr, "Wrote output file %q\n", bulkOutfile)
	}
}
