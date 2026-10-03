variable "TAG" {
  default = "local"
}
variable "REPO" {
  default = "ghcr.io/lesomnus/xenomai-exporter"
}
variable "BUILD_HASH" {
  default = "0000000000000000000000000000000000000000"
}
variable "BUILD_TIMESTAMP" {
  default = "${timestamp()}"
}
variable "BUILD_YY" {
  default = "${formatdate("YY", BUILD_TIMESTAMP)}"
}
variable "BUILD_YYMM" {
  default = "${formatdate("YYMM", BUILD_TIMESTAMP)}"
}
variable "BUILD_DATE" {
  default = "${formatdate("YYMMDD", BUILD_TIMESTAMP)}"
}
variable "BUILD_ID" {
  default = "r0"
}

target "test" {
  target = "test"
}
target "build" {
  target = "build"
  args = {
    BUILD_HASH = BUILD_HASH
    BUILD_DATE = BUILD_DATE
    BUILD_ID   = BUILD_ID
  }
  output = [{
    type = "local"
    dest = "build"
  }]
}
target "app" {
  target = "app"
  context = "./build"
  dockerfile = "../Dockerfile"
  attest = [
    "type=provenance,mode=max",
    "type=sbom",
  ]
  labels = {
    "org.opencontainers.image.title"       = "xenomai-exporter"
    "org.opencontainers.image.description" = "Reads the statistics of the Xenomai Cobalt core from /proc/xenomai and exports them via OpenTelemetry"
    "org.opencontainers.image.url"         = "https://github.com/lesomnus/xenomai-exporter"
    "org.opencontainers.image.source"      = "https://github.com/lesomnus/xenomai-exporter"
    "org.opencontainers.image.revision"    = BUILD_HASH
    "org.opencontainers.image.created"     = BUILD_TIMESTAMP
    "org.opencontainers.image.version"     = "${BUILD_DATE}-${BUILD_ID}"
  }
  tags = [
    "${REPO}:${TAG}",
    "${REPO}:${BUILD_ID}",
    "${REPO}:${BUILD_YY}",
    "${REPO}:${BUILD_YY}-${BUILD_ID}",
    "${REPO}:${BUILD_YYMM}",
    "${REPO}:${BUILD_YYMM}-${BUILD_ID}",
    "${REPO}:${BUILD_DATE}",
    "${REPO}:${BUILD_DATE}-${BUILD_ID}",
    "${REPO}:${BUILD_HASH}",
  ]
}
