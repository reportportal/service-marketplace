package com.epam.reportportal.marketplace.web.error;

public class UnauthorizedException extends MarketplaceException {

  public UnauthorizedException(String message) {
    super("UNAUTHORIZED", message);
  }
}
