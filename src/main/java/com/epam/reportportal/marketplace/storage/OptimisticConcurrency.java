package com.epam.reportportal.marketplace.storage;

import java.util.function.Supplier;

public final class OptimisticConcurrency {

  private static final int MAX_RETRIES = 3;

  private OptimisticConcurrency() {}

  public static void execute(Runnable operation) {
    execute(() -> {
      operation.run();
      return null;
    });
  }

  public static <T> T execute(Supplier<T> operation) {
    ConcurrentModificationException last = null;
    for (int attempt = 0; attempt < MAX_RETRIES; attempt++) {
      try {
        return operation.get();
      } catch (ConcurrentModificationException e) {
        last = e;
      }
    }
    throw last != null ? last : new ConcurrentModificationException("Concurrent modification after retries");
  }
}
