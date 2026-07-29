package com.epam.reportportal.marketplace.web;

import com.epam.reportportal.marketplace.service.AdvisoryService;
import com.epam.reportportal.marketplace.web.dto.AttachAdvisoryRequestDto;
import com.epam.reportportal.marketplace.web.dto.SecurityAdvisoryDto;
import jakarta.validation.Valid;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/api/v1/plugins/{pluginId}/versions/{version}/advisory")
public class AdvisoryController {

  private final AdvisoryService advisoryService;

  public AdvisoryController(AdvisoryService advisoryService) {
    this.advisoryService = advisoryService;
  }

  @PostMapping
  SecurityAdvisoryDto attachAdvisory(
      @PathVariable String pluginId,
      @PathVariable String version,
      @Valid @RequestBody AttachAdvisoryRequestDto request) {
    return advisoryService.attachAdvisory(pluginId, version, request.severity(), request.text());
  }
}
