package com.epam.reportportal.marketplace.storage;

public class ConcurrentModificationException extends ObjectStoreException {

  public ConcurrentModificationException(String message) {
    super(message);
  }
}
