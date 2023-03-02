# gcp-rss-notifier

This is a serverless solution that retrieves RSS feeds for new items and sent to a Google Chat
webhook URL for notification.

## Architecture

![arch.png][doc/arch.png]

### Resources

- **List Channels Function** - Cloud Function that lists all channels of RSS feeds for processing
- **Process Channel Function** - Cloud Function that checks the RSS feed for new content
- **Process Item Function** - Cloud Function that sends a message to Google Chat for each new item
- **Channel Topic** - Pub/Sub topic that dispatches a message for each RSS feed
- **Item Topic** - Pub/Sub topic that dispatches a message for each new item in an RSS feed
- **Channels Table** - Firestore table that holds configuration of all RSS feeds
- **Items table** - Firestore table that record all RSS items that have been processed
