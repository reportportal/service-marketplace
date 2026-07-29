package com.epam.reportportal.marketplace.storage;

public class ObjectStoreException extends RuntimeException {

  public ObjectStoreException(String message) {
    super(message);
  }

  public ObjectStoreException(String message, Throwable cause) {
    super(message, cause);
  }
}
