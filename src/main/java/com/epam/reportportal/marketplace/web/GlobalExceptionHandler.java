package com.epam.reportportal.marketplace.web;

import com.epam.reportportal.marketplace.web.dto.ErrorResponseDto;
import com.epam.reportportal.marketplace.web.dto.ValidationErrorResponseDto;
import com.epam.reportportal.marketplace.web.dto.ValidationFieldError;
import com.epam.reportportal.marketplace.web.error.ConflictException;
import com.epam.reportportal.marketplace.web.error.ForbiddenException;
import com.epam.reportportal.marketplace.web.error.GoneException;
import com.epam.reportportal.marketplace.web.error.NotFoundException;
import com.epam.reportportal.marketplace.web.error.ServiceUnavailableException;
import com.epam.reportportal.marketplace.web.error.TooManyRequestsException;
import com.epam.reportportal.marketplace.web.error.UnauthorizedException;
import com.epam.reportportal.marketplace.web.error.ValidationException;
import java.util.List;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.http.converter.HttpMessageNotReadableException;
import org.springframework.validation.FieldError;
import org.springframework.web.bind.MethodArgumentNotValidException;
import org.springframework.web.bind.annotation.ExceptionHandler;
import org.springframework.web.bind.annotation.RestControllerAdvice;
import org.springframework.web.multipart.MaxUploadSizeExceededException;
import org.springframework.web.server.ContentTooLargeException;

@RestControllerAdvice
public class GlobalExceptionHandler {

  private static final Logger LOGGER = LoggerFactory.getLogger(GlobalExceptionHandler.class);

  @ExceptionHandler(NotFoundException.class)
  ResponseEntity<ErrorResponseDto> notFound(NotFoundException ex) {
    return ResponseEntity.status(HttpStatus.NOT_FOUND).body(new ErrorResponseDto(ex.getCode(), ex.getMessage()));
  }

  @ExceptionHandler(UnauthorizedException.class)
  ResponseEntity<ErrorResponseDto> unauthorized(UnauthorizedException ex) {
    return ResponseEntity.status(HttpStatus.UNAUTHORIZED).body(new ErrorResponseDto(ex.getCode(), ex.getMessage()));
  }

  @ExceptionHandler(TooManyRequestsException.class)
  ResponseEntity<ErrorResponseDto> tooManyRequests(TooManyRequestsException ex) {
    return ResponseEntity.status(HttpStatus.TOO_MANY_REQUESTS)
        .body(new ErrorResponseDto(ex.getCode(), ex.getMessage()));
  }

  @ExceptionHandler(ForbiddenException.class)
  ResponseEntity<?> forbidden(ForbiddenException ex) {
    if (ex.getPayload() != null) {
      return ResponseEntity.status(HttpStatus.FORBIDDEN).body(ex.getPayload());
    }
    return ResponseEntity.status(HttpStatus.FORBIDDEN).body(new ErrorResponseDto(ex.getCode(), ex.getMessage()));
  }

  @ExceptionHandler(GoneException.class)
  ResponseEntity<?> gone(GoneException ex) {
    return ResponseEntity.status(HttpStatus.GONE).body(ex.getTombstone());
  }

  @ExceptionHandler(ValidationException.class)
  ResponseEntity<ValidationErrorResponseDto> validation(ValidationException ex) {
    return ResponseEntity.status(HttpStatus.UNPROCESSABLE_CONTENT)
        .body(new ValidationErrorResponseDto(ex.getCode(), ex.getMessage(), ex.getErrors()));
  }

  @ExceptionHandler(ServiceUnavailableException.class)
  ResponseEntity<ErrorResponseDto> serviceUnavailable(ServiceUnavailableException ex) {
    LOGGER.warn("Rejected request for a disabled registry capability: {}", ex.getMessage());
    return ResponseEntity.status(HttpStatus.SERVICE_UNAVAILABLE)
        .body(new ErrorResponseDto(ex.getCode(), ex.getMessage()));
  }

  @ExceptionHandler(ConflictException.class)
  ResponseEntity<ErrorResponseDto> conflict(ConflictException ex) {
    return ResponseEntity.status(HttpStatus.CONFLICT).body(new ErrorResponseDto(ex.getCode(), ex.getMessage()));
  }

  @ExceptionHandler(MethodArgumentNotValidException.class)
  ResponseEntity<ValidationErrorResponseDto> beanValidation(MethodArgumentNotValidException ex) {
    List<ValidationFieldError> errors = ex.getBindingResult().getFieldErrors().stream()
        .map(this::toFieldError)
        .toList();
    return ResponseEntity.status(HttpStatus.UNPROCESSABLE_CONTENT)
        .body(new ValidationErrorResponseDto("VALIDATION_ERROR", "Request validation failed", errors));
  }

  @ExceptionHandler({MaxUploadSizeExceededException.class, ContentTooLargeException.class})
  ResponseEntity<ErrorResponseDto> uploadTooLarge(Exception ex) {
    LOGGER.warn("Rejected publish bundle exceeding the configured upload limit", ex);
    return ResponseEntity.status(HttpStatus.CONTENT_TOO_LARGE)
        .body(new ErrorResponseDto("PAYLOAD_TOO_LARGE",
            "Publish bundle exceeds the maximum upload size accepted by the registry"));
  }

  @ExceptionHandler(HttpMessageNotReadableException.class)
  ResponseEntity<ErrorResponseDto> malformedBody(HttpMessageNotReadableException ex) {
    LOGGER.warn("Rejected unreadable request body: {}", ex.getMessage());
    return ResponseEntity.status(HttpStatus.BAD_REQUEST)
        .body(new ErrorResponseDto("BAD_REQUEST", "Request body could not be parsed"));
  }

  @ExceptionHandler(Exception.class)
  ResponseEntity<ErrorResponseDto> internal(Exception ex) {
    LOGGER.error("Unhandled exception while processing registry request", ex);
    return ResponseEntity.status(HttpStatus.INTERNAL_SERVER_ERROR)
        .body(new ErrorResponseDto("INTERNAL_ERROR", "Unexpected registry error"));
  }

  private ValidationFieldError toFieldError(FieldError error) {
    return new ValidationFieldError(error.getField(), error.getDefaultMessage());
  }
}
