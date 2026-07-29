package com.epam.reportportal.marketplace.web.error;

public class ConflictException extends MarketplaceException {

  public ConflictException(String message) {
    super("CONFLICT", message);
  }
}
