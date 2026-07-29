package com.epam.reportportal.marketplace.service;

import java.util.List;

public interface CdnInvalidationService {

  void invalidatePaths(List<String> paths);
}
