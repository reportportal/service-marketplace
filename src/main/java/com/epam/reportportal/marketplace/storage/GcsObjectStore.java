package com.epam.reportportal.marketplace.storage;

import com.epam.reportportal.marketplace.config.MarketplaceProperties;
import com.google.cloud.storage.Blob;
import com.google.cloud.storage.BlobId;
import com.google.cloud.storage.BlobInfo;
import com.google.cloud.storage.Storage;
import com.google.cloud.storage.StorageOptions;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import java.util.Optional;
import java.util.concurrent.TimeUnit;

public class GcsObjectStore implements ObjectStore {

  private final Storage storage;
  private final String bucket;

  public GcsObjectStore(MarketplaceProperties properties) {
    this.storage = StorageOptions.getDefaultInstance().getService();
    this.bucket = properties.getGcs().getBucket();
    if (bucket == null || bucket.isBlank()) {
      throw new ObjectStoreException("GCS bucket must be configured when storage.type=gcs");
    }
  }

  @Override
  public byte[] readBytes(String key) {
    return read(key).data();
  }

  @Override
  public StoredObject read(String key) {
    Blob blob = storage.get(BlobId.of(bucket, key));
    if (blob == null) {
      throw new ObjectStoreException("Object not found: " + key);
    }
    return new StoredObject(blob.getContent(), blob.getGeneration());
  }

  @Override
  public void writeBytes(String key, byte[] data) {
    storage.create(BlobInfo.newBuilder(bucket, key).build(), data);
  }

  @Override
  public void writeBytesIfGenerationMatch(String key, byte[] data, long expectedGeneration) {
    BlobInfo info = BlobInfo.newBuilder(bucket, key).build();
    Storage.BlobTargetOption option =
        expectedGeneration >= 0
            ? Storage.BlobTargetOption.generationMatch(expectedGeneration)
            : Storage.BlobTargetOption.doesNotExist();
    try {
      storage.create(info, data, option);
    } catch (com.google.cloud.storage.StorageException e) {
      if (e.getCode() == 412) {
        throw new ConcurrentModificationException("Generation mismatch for " + key);
      }
      throw new ObjectStoreException("Failed to write GCS object: " + key, e);
    }
  }

  @Override
  public void delete(String key) {
    storage.delete(BlobId.of(bucket, key));
  }

  @Override
  public List<String> listPrefix(String prefix) {
    List<String> keys = new ArrayList<>();
    for (Blob blob : storage.list(bucket, Storage.BlobListOption.prefix(prefix)).iterateAll()) {
      if (!blob.isDirectory()) {
        keys.add(blob.getName());
      }
    }
    return keys;
  }

  @Override
  public boolean exists(String key) {
    Blob blob = storage.get(BlobId.of(bucket, key));
    return blob != null && blob.exists();
  }

  @Override
  public Optional<Long> getGeneration(String key) {
    Blob blob = storage.get(BlobId.of(bucket, key));
    if (blob == null) {
      return Optional.empty();
    }
    return Optional.of(blob.getGeneration());
  }

  @Override
  public SignedUrl createSignedUrl(String key, Duration maxTtl) {
    Duration ttl = maxTtl.compareTo(Duration.ofSeconds(60)) > 0 ? Duration.ofSeconds(60) : maxTtl;
    BlobInfo blobInfo = BlobInfo.newBuilder(bucket, key).build();
    java.net.URL url =
        storage.signUrl(
            blobInfo,
            ttl.toSeconds(),
            TimeUnit.SECONDS,
            Storage.SignUrlOption.withV4Signature());
    return new SignedUrl(url.toString(), Instant.now().plus(ttl));
  }
}
