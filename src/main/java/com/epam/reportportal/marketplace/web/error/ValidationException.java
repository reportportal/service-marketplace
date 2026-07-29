package com.epam.reportportal.marketplace.web.error;

import com.epam.reportportal.marketplace.web.dto.ValidationFieldError;
import java.util.List;

public class ValidationException extends MarketplaceException {

  private final List<ValidationFieldError> errors;

  public ValidationException(String message, List<ValidationFieldError> errors) {
    super("VALIDATION_ERROR", message);
    this.errors = errors;
  }

  public List<ValidationFieldError> getErrors() {
    return errors;
  }
}
