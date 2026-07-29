package com.epam.reportportal.marketplace.storage;

import java.time.Duration;
import java.util.List;
import java.util.Optional;

public interface ObjectStore {

  byte[] readBytes(String key);

  StoredObject read(String key);

  void writeBytes(String key, byte[] data);

  void writeBytesIfGenerationMatch(String key, byte[] data, long expectedGeneration);

  void delete(String key);

  List<String> listPrefix(String prefix);

  boolean exists(String key);

  Optional<Long> getGeneration(String key);

  SignedUrl createSignedUrl(String key, Duration maxTtl);

  default boolean verifySignedUrl(String key, long expiresAtEpochSecond, String signature) {
    return false;
  }

  record SignedUrl(String url, java.time.Instant expiresAt) {}

  record StoredObject(byte[] data, long generation) {}
}
