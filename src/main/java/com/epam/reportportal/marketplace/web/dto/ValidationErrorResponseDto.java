package com.epam.reportportal.marketplace.web.dto;

import java.util.List;

public record ValidationErrorResponseDto(String code, String message, List<ValidationFieldError> errors) {}
