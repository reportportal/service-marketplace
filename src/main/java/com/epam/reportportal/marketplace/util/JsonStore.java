package com.epam.reportportal.marketplace.util;

import com.epam.reportportal.marketplace.storage.ObjectStore;
import tools.jackson.core.type.TypeReference;
import tools.jackson.databind.ObjectMapper;

public final class JsonStore {

  private JsonStore() {}

  public static <T> T read(ObjectStore store, ObjectMapper mapper, String key, Class<T> type) {
    if (!store.exists(key)) {
      return null;
    }
    try {
      return mapper.readValue(store.readBytes(key), type);
    } catch (RuntimeException e) {
      throw new IllegalStateException("Failed to deserialize " + key, e);
    }
  }

  public static <T> T read(ObjectStore store, ObjectMapper mapper, String key, TypeReference<T> type) {
    if (!store.exists(key)) {
      return null;
    }
    try {
      return mapper.readValue(store.readBytes(key), type);
    } catch (RuntimeException e) {
      throw new IllegalStateException("Failed to deserialize " + key, e);
    }
  }

  public static void write(ObjectStore store, ObjectMapper mapper, String key, Object value) {
    try {
      store.writeBytes(key, mapper.writerWithDefaultPrettyPrinter().writeValueAsBytes(value));
    } catch (RuntimeException e) {
      throw new IllegalStateException("Failed to serialize " + key, e);
    }
  }

  public static void writeIfGenerationMatch(
      ObjectStore store, ObjectMapper mapper, String key, Object value, long generation) {
    try {
      store.writeBytesIfGenerationMatch(
          key, mapper.writerWithDefaultPrettyPrinter().writeValueAsBytes(value), generation);
    } catch (RuntimeException e) {
      throw new IllegalStateException("Failed to serialize " + key, e);
    }
  }
}
