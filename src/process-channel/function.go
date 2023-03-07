package function

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"cloud.google.com/go/firestore"
	"cloud.google.com/go/pubsub"
	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	"github.com/cloudevents/sdk-go/v2/event"
	"github.com/mmcdole/gofeed"
)

var itemTopic string
var projectID string
var collection string

func init() {
	functions.CloudEvent("ProcessChannel", processChannel)
	itemTopic = os.Getenv("ITEM_TOPIC")
	projectID = os.Getenv("PROJECT_ID")
	collection = os.Getenv("COLLECTION")
}

// MessagePublishedData contains the full Pub/Sub message
// See the documentation for more details:
// https://cloud.google.com/eventarc/docs/cloudevents#pubsub
type MessagePublishedData struct {
	Message PubSubMessage
}

// PubSubMessage is the payload of a Pub/Sub event.
// See the documentation for more details:
// https://cloud.google.com/pubsub/docs/reference/rest/v1/PubsubMessage
type PubSubMessage struct {
	Data []byte `json:"data"`
}

type ChannelConfig struct {
	FeedURL   string `json:"url"`
	NotifyURL string `json:"notify"`
}

type FeedItem struct {
	NotifyURL string `json:"notify"`
	ID        string `json:"id"`
	Updated   string `json:"updated"`
	Link      string `json:"link"`
	Title     string `json:"title"`
	Content   string `json:"content"`
}

func processChannel(ctx context.Context, e event.Event) error {
	var msg MessagePublishedData
	if err := e.DataAs(&msg); err != nil {
		return fmt.Errorf("event.DataAs: %v", err)
	}

	channelConfig := ChannelConfig{}
	err := json.Unmarshal(msg.Message.Data, &channelConfig)
	if err != nil {
		return fmt.Errorf("failed parsing JSON: %v", err)
	}

	fp := gofeed.NewParser()
	feed, err := fp.ParseURL(channelConfig.FeedURL)
	if err != nil {
		return fmt.Errorf("failed parsing feed: %v", err)
	}

	items, err := removeOldItems(ctx, feed.Items)
	if err != nil {
		return fmt.Errorf("failed processing items: %v", err)
	}
	publishItem(ctx, channelConfig.NotifyURL, items)

	return nil
}

func publishItem(ctx context.Context, notifyURL string, items []*gofeed.Item) error {
	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return fmt.Errorf("pubsub.NewClient: %v", err)
	}
	defer client.Close()

	var wg sync.WaitGroup
	var totalErrors uint64
	t := client.Topic(itemTopic)

	for _, item := range items {
		feed := FeedItem{
			NotifyURL: notifyURL,
			ID:        item.GUID,
			Title:     item.Title,
			Content:   item.Content,
			Link:      item.Link,
			Updated:   item.Updated,
		}
		itemJson, err := json.Marshal(feed)
		if err != nil {
			return fmt.Errorf("json.Marshal: %v", err)
		}

		result := t.Publish(ctx, &pubsub.Message{
			Data: []byte(itemJson),
		})

		wg.Add(1)
		go func(res *pubsub.PublishResult) {
			defer wg.Done()
			// The Get method blocks until a server-generated ID or
			// an error is returned for the published message.
			id, err := res.Get(ctx)
			if err != nil {
				// Error handling code can be added here.
				fmt.Printf("Failed to publish: %v\n", err)
				atomic.AddUint64(&totalErrors, 1)
				return
			}
			fmt.Printf("Published message to topic %s; msg ID: %v\n", itemTopic, id)
		}(result)
	}

	wg.Wait()

	if totalErrors > 0 {
		return fmt.Errorf("%d of %d messages did not publish successfuly", totalErrors, len(items))
	}

	return nil
}

func removeOldItems(ctx context.Context, items []*gofeed.Item) ([]*gofeed.Item, error) {
	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("firestore client error: %v", err)
	}
	defer client.Close()

	var ids []*firestore.DocumentRef
	itemMap := make(map[string]*gofeed.Item)
	total := len(items)
	for _, item := range items {
		ids = append(ids, client.Collection(collection).Doc(item.GUID))
		itemMap[item.GUID] = item
	}

	docs, err := client.GetAll(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("failed getting items from firestore: %v", err)
	}

	for _, snap := range docs {
		doc := snap.Ref
		if snap.Data() != nil {
			delete(itemMap, doc.ID)
		}
	}

	notFound := make([]*gofeed.Item, 0, len(itemMap))
	for _, v := range itemMap {
		notFound = append(notFound, v)
	}

	fmt.Printf("%d of %d items are new\n", len(notFound), total)

	return notFound, nil
}
