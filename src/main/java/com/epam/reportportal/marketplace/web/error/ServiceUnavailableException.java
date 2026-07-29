package com.epam.reportportal.marketplace.web.error;

public class ServiceUnavailableException extends MarketplaceException {

  public ServiceUnavailableException(String message) {
    super("SERVICE_UNAVAILABLE", message);
  }
}
