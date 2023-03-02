package function

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"cloud.google.com/go/firestore"
	"cloud.google.com/go/pubsub"
	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	"google.golang.org/api/iterator"
)

var channelTopic string
var projectID string
var collection string

func init() {
	functions.HTTP("ListChannels", listChannels)
	channelTopic = os.Getenv("CHANNEL_TOPIC")
	projectID = os.Getenv("PROJECT_ID")
	collection = os.Getenv("COLLECTION")
}

func listChannels(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		log.Fatalf("firestore client error: %v", err)
	}
	defer client.Close()

	fmt.Printf("Getting all channels from %s\n", collection)
	iter := client.Collection(collection).Documents(ctx)

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Fatalf("firestore collection error: %v", err)
		}

		channel := doc.Data()
		err = publishChannel(ctx, channel)
		if err != nil {
			log.Printf("publish channel error: %v", err)
		}
	}

	fmt.Fprintf(w, "Success")
}

func publishChannel(ctx context.Context, channel map[string]interface{}) error {
	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return fmt.Errorf("pubsub.NewClient: %v", err)
	}
	defer client.Close()

	t := client.Topic(channelTopic)

	channelJson, err := json.Marshal(channel)
	if err != nil {
		return fmt.Errorf("json.Marshal: %v", err)
	}

	result := t.Publish(ctx, &pubsub.Message{
		Data: []byte(channelJson),
	})

	// The Get method blocks until a server-generated ID or
	// an error is returned for the published message.
	id, err := result.Get(ctx)
	if err != nil {
		// Error handling code can be added here.
		return fmt.Errorf("failed to publish: %v", err)
	}
	fmt.Printf("Published message to topic %s; msg ID: %v\n", channelTopic, id)

	return nil
}
