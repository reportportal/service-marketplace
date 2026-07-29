package com.epam.reportportal.marketplace.web.error;

public class TooManyRequestsException extends MarketplaceException {

  public TooManyRequestsException(String message) {
    super("TOO_MANY_REQUESTS", message);
  }
}
