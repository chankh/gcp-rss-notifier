data "google_project" "project" {
  project_id = var.project_id
}

resource "random_id" "suffix" {
  byte_length = 2
}

locals {
  pubsub_sa_email = "service-${data.google_project.project.number}@gcp-sa-pubsub.iam.gserviceaccount.com"
}

module "project_services" {
  source                      = "terraform-google-modules/project-factory/google//modules/project_services"
  version                     = "~> 13.0"
  disable_services_on_destroy = false
  project_id                  = var.project_id
  enable_apis                 = var.enable_apis

  activate_apis = [
    "artifactregistry.googleapis.com",
    "cloudbuild.googleapis.com",
    "cloudfunctions.googleapis.com",
    "cloudscheduler.googleapis.com",
    "compute.googleapis.com",
    "eventarc.googleapis.com",
    "firestore.googleapis.com",
    "iam.googleapis.com",
    "pubsub.googleapis.com",
    "run.googleapis.com",
    "storage-api.googleapis.com",
    "storage.googleapis.com",
  ]
}

resource "google_project_service_identity" "service_identities" {
  provider = google-beta
  project  = module.project_services.project_id

  for_each = toset([
    "pubsub.googleapis.com",
  ])
  service = each.value
}

resource "google_storage_bucket" "gcf_source" {
  name                        = "gcf-source-${random_id.suffix.hex}"
  project                     = module.project_services.project_id
  location                    = var.region
  force_destroy               = false
  uniform_bucket_level_access = true
}

# Allow the pubsub service account on this project to create identity tokens
resource "google_project_iam_member" "pubsub_token_creator" {
  project = module.project_services.project_id
  role    = "roles/iam.serviceAccountTokenCreator"
  member = "serviceAccount:${local.pubsub_sa_email}"
}

resource "google_service_account" "rss_notifier_sa" {
  account_id   = "rss-notifier-sa-${random_id.suffix.hex}"
  project      = module.project_services.project_id
  display_name = <<-EOT
  Service account used by RSS Notifier
  EOT
}

resource "google_project_iam_member" "run-invoking" {
  project = module.project_services.project_id
  role    = "roles/run.invoker"
  member  = "serviceAccount:${google_service_account.rss_notifier_sa.email}"
}

resource "google_project_iam_member" "event-receiving" {
  project = module.project_services.project_id
  role    = "roles/eventarc.eventReceiver"
  member  = "serviceAccount:${google_service_account.rss_notifier_sa.email}"
}

resource "google_project_iam_member" "artifactregistry-reader" {
  project = module.project_services.project_id
  role    = "roles/artifactregistry.reader"
  member  = "serviceAccount:${google_service_account.rss_notifier_sa.email}"
}

resource "google_project_iam_member" "pubsub-publisher" {
  project = module.project_services.project_id
  role    = "roles/pubsub.publisher"
  member  = "serviceAccount:${google_service_account.rss_notifier_sa.email}"
}

resource "google_project_iam_member" "firestore-user" {
  project = module.project_services.project_id
  role    = "roles/datastore.user"
  member  = "serviceAccount:${google_service_account.rss_notifier_sa.email}"
}

data "archive_file" "list-channels" {
  type        = "zip"
  source_dir  = "${path.module}/src/list-channels"
  output_path = "${path.module}/build/list-channels.zip"
}

data "archive_file" "process-channel" {
  type        = "zip"
  source_dir  = "${path.module}/src/process-channel"
  output_path = "${path.module}/build/process-channel.zip"
}

data "archive_file" "process-item" {
  type        = "zip"
  source_dir  = "${path.module}/src/process-item"
  output_path = "${path.module}/build/process-item.zip"
}

# upload the list-channels function source zipfile to cloud storage
resource "google_storage_bucket_object" "list-channels_source" {
  name   = "list-channels.zip"
  source = data.archive_file.list-channels.output_path
  bucket = google_storage_bucket.gcf_source.name
}

# upload the process-channel function source zipfile to cloud storage
resource "google_storage_bucket_object" "process-channel_source" {
  name   = "process-channel.zip"
  source = data.archive_file.process-channel.output_path
  bucket = google_storage_bucket.gcf_source.name
}

# upload the process-item function source zipfile to cloud storage
resource "google_storage_bucket_object" "process-item_source" {
  name   = "process-item.zip"
  source = data.archive_file.process-item.output_path
  bucket = google_storage_bucket.gcf_source.name
}

resource "google_cloudfunctions2_function" "list-channels" {
  provider    = google-beta
  project     = module.project_services.project_id
  location    = var.region
  name        = "rss-list-channels-${random_id.suffix.hex}"
  description = <<-EOT
  Cloud function that gets all RSS channels from Firestore and
  sends items to a Pub/Sub topic
  EOT


  build_config {
    runtime     = "go125"
    entry_point = "ListChannels"
    source {
      storage_source {
        bucket = google_storage_bucket.gcf_source.name
        object = google_storage_bucket_object.list-channels_source.output_name
      }
    }
  }

  service_config {
    max_instance_count = 3
    min_instance_count = 0
    available_memory   = "128Mi"
    timeout_seconds    = 60
    environment_variables = {
      CHANNEL_TOPIC = google_pubsub_topic.channel-topic.name
      PROJECT_ID = module.project_services.project_id
      COLLECTION = "rss-channels"
    }
    ingress_settings               = "ALLOW_INTERNAL_ONLY"
    all_traffic_on_latest_revision = true
    service_account_email          = google_service_account.rss_notifier_sa.email
  }

  depends_on = [
    google_project_iam_member.artifactregistry-reader,
    google_project_iam_member.event-receiving,
  ]
}

resource "google_cloudfunctions2_function" "process-channel" {
  provider    = google-beta
  project     = module.project_services.project_id
  location    = var.region
  name        = "rss-process-channel-${random_id.suffix.hex}"
  description = <<-EOT
  Cloud function that process a RSS channel received from Pub/Sub and
  sends items to a Pub/Sub topic
  EOT


  build_config {
    runtime     = "go125"
    entry_point = "ProcessChannel"
    source {
      storage_source {
        bucket = google_storage_bucket.gcf_source.name
        object = google_storage_bucket_object.process-channel_source.output_name
      }
    }
  }

  service_config {
    max_instance_count = 10
    min_instance_count = 0
    available_memory   = "128Mi"
    timeout_seconds    = 60
    environment_variables = {
      ITEM_TOPIC = google_pubsub_topic.item-topic.name
      PROJECT_ID = module.project_services.project_id
      COLLECTION = "rss-items"
    }
    ingress_settings               = "ALLOW_INTERNAL_ONLY"
    all_traffic_on_latest_revision = true
    service_account_email          = google_service_account.rss_notifier_sa.email
  }

  event_trigger {
      trigger_region = var.region
      event_type            = "google.cloud.pubsub.topic.v1.messagePublished"
      pubsub_topic          = google_pubsub_topic.channel-topic.id
      retry_policy          = "RETRY_POLICY_RETRY"
      service_account_email = google_service_account.rss_notifier_sa.email
  }

  depends_on = [
    google_project_iam_member.artifactregistry-reader,
    google_project_iam_member.event-receiving,
  ]
}

resource "google_cloudfunctions2_function" "process-item" {
  provider    = google-beta
  project     = module.project_services.project_id
  location    = var.region
  name        = "rss-process-item-${random_id.suffix.hex}"
  description = <<-EOT
  Cloud function that process a RSS feed item received from Pub/Sub and
  send notification
  EOT


  build_config {
    runtime     = "go125"
    entry_point = "ProcessItem"
    source {
      storage_source {
        bucket = google_storage_bucket.gcf_source.name
        object = google_storage_bucket_object.process-item_source.output_name
      }
    }
  }

  service_config {
    max_instance_count = 10
    min_instance_count = 0
    available_memory   = "128Mi"
    timeout_seconds    = 60
    environment_variables = {
      PROJECT_ID = module.project_services.project_id
      COLLECTION = "rss-items"
    }
    ingress_settings               = "ALLOW_INTERNAL_ONLY"
    all_traffic_on_latest_revision = true
    service_account_email          = google_service_account.rss_notifier_sa.email
  }

  event_trigger {
      trigger_region = var.region
      event_type            = "google.cloud.pubsub.topic.v1.messagePublished"
      pubsub_topic          = google_pubsub_topic.item-topic.id
      retry_policy          = "RETRY_POLICY_RETRY"
      service_account_email = google_service_account.rss_notifier_sa.email
  }

  depends_on = [
    google_project_iam_member.artifactregistry-reader,
    google_project_iam_member.event-receiving,
  ]
}

resource "google_pubsub_topic" "channel-topic" {
  name = "rss-channel-topic-${random_id.suffix.hex}"
}

resource "google_pubsub_topic" "item-topic" {
  name = "rss-item-topic-${random_id.suffix.hex}"
}

resource "google_firestore_database" "rss_database" {
  provider    = google-beta
  project     = module.project_services.project_id
  location_id = var.region

  name        = "(default)"

  type        = "FIRESTORE_NATIVE"
}

resource "google_cloud_scheduler_job" "check-feeds-job" {
  name        = "check-feeds-job-${random_id.suffix.hex}"
  description = "Check RSS feeds"
  region      = var.region
  schedule    = "*/5 * * * *"

  http_target {
    http_method = "POST"
    uri         = google_cloudfunctions2_function.list-channels.service_config[0].uri
    body        = base64encode("{\"event\":\"trigger\"}")

    oidc_token {
        service_account_email = google_service_account.rss_notifier_sa.email
    }
  }
}
