package com.epam.reportportal.marketplace.web.error;

public class NotFoundException extends MarketplaceException {

  public NotFoundException(String message) {
    super("NOT_FOUND", message);
  }
}
