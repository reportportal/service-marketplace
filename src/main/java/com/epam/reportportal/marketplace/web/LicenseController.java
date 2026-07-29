package com.epam.reportportal.marketplace.web;

import com.epam.reportportal.marketplace.service.LicenseService;
import com.epam.reportportal.marketplace.web.dto.CreateLicenseRequestDto;
import com.epam.reportportal.marketplace.web.dto.CreateLicenseResponseDto;
import com.epam.reportportal.marketplace.web.dto.LicenseEntitlementListResponseDto;
import com.epam.reportportal.marketplace.web.dto.RotateLicenseKeyResponseDto;
import jakarta.validation.Valid;
import org.springframework.http.HttpStatus;
import org.springframework.web.bind.annotation.DeleteMapping;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.ResponseStatus;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/api/v1/licenses")
public class LicenseController {

  private final LicenseService licenseService;

  public LicenseController(LicenseService licenseService) {
    this.licenseService = licenseService;
  }

  @GetMapping
  LicenseEntitlementListResponseDto listLicenses() {
    return licenseService.listEntitlements();
  }

  @PostMapping
  @ResponseStatus(HttpStatus.CREATED)
  CreateLicenseResponseDto createLicense(@Valid @RequestBody CreateLicenseRequestDto request) {
    return licenseService.createEntitlement(request.customerId(), request.expiresAt());
  }

  @DeleteMapping("/{customerId}")
  @ResponseStatus(HttpStatus.NO_CONTENT)
  void revokeLicense(@PathVariable String customerId) {
    licenseService.revokeEntitlement(customerId);
  }

  @PostMapping("/{customerId}/keys")
  @ResponseStatus(HttpStatus.CREATED)
  RotateLicenseKeyResponseDto rotateKey(@PathVariable String customerId) {
    return licenseService.rotateKey(customerId);
  }
}
