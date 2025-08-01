# Midas - DCF Valuation API

> Professional-grade REST API for equity valuation using Discounted Cash Flow analysis and real-time financial data

[![Go Version](https://img.shields.io/badge/Go-1.22%2B-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

## 🎯 Overview

Midas provides institutional-quality equity valuation through a simple REST API. It combines SEC financial data, market prices, and macroeconomic indicators to calculate intrinsic value using industry best practices.

### Key Capabilities

- **📊 Intrinsic Valuation** - Net tangible asset value and DCF fair value per share
- **🔄 Real-time Data Integration** - SEC EDGAR, Yahoo Finance, and Federal Reserve data
- **🧹 Financial Data Normalization** - Removes accounting distortions and adjusts for one-time items
- **🏭 Industry-Specific Analysis** - Tailored adjustments for technology, finance, healthcare, and more
- **⚡ Production Ready** - Built-in caching, rate limiting, and monitoring
- **🔐 Enterprise Security** - API key authentication and request throttling

## 🚀 Quick Start

### Prerequisites

- Go 1.22 or higher
- Docker & Docker Compose
- Git

### Installation

```bash
# Clone the repository
git clone https://github.com/your-org/midas.git
cd midas

# Launch the staging environment (single command)
./scripts/launch_staging.sh  # Linux/macOS
# OR
.\scripts\launch_staging.ps1  # Windows PowerShell
```

The launch script automatically:
- ✅ Creates configuration from template
- ✅ Starts Redis cache
- ✅ Builds and starts the API server
- ✅ Verifies health status
- ✅ Displays connection details

## 📖 Usage

### Authentication

All API requests require authentication via Bearer token:

```bash
Authorization: Bearer your-api-key-here
```

For staging/demo: `Bearer demo-key-phase-2.5-mvp`

### API Endpoints

#### Get Fair Value for Single Ticker

```bash
GET /api/v1/fair-value/{ticker}
```

**Example Request:**
```bash
curl -H "Authorization: Bearer demo-key-phase-2.5-mvp" \
  http://localhost:8080/api/v1/fair-value/AAPL
```

**Example Response:**
```json
{
  "ticker": "AAPL",
  "analysis_date": "2024-01-31T10:30:00Z",
  "market_data": {
    "current_price": 185.50,
    "market_cap": 2850000000000,
    "shares_outstanding": 15364718000
  },
  "valuation": {
    "net_tangible_asset_value_per_share": 3.47,
    "dcf_fair_value_per_share": 167.23,
    "margin_of_safety": -0.098,
    "valuation_method": "DCF_5_YEAR"
  },
  "financial_metrics": {
    "ttm_revenue": 383285000000,
    "ttm_fcf": 99834000000,
    "revenue_growth_rate": 0.033,
    "wacc": 0.095,
    "terminal_growth_rate": 0.025
  },
  "quality_score": {
    "overall": 0.92,
    "data_completeness": 0.95,
    "earnings_quality": 0.89,
    "adjustment_transparency": 0.91
  }
}
```

#### Bulk Valuation Request

```bash
POST /api/v1/bulk
```

**Example Request:**
```bash
curl -X POST -H "Authorization: Bearer demo-key-phase-2.5-mvp" \
  -H "Content-Type: application/json" \
  -d '{
    "tickers": ["AAPL", "MSFT", "GOOGL"],
    "include_details": true
  }' \
  http://localhost:8080/api/v1/bulk
```

**Example Response:**
```json
{
  "results": [
    {
      "ticker": "AAPL",
      "dcf_fair_value_per_share": 167.23,
      "net_tangible_asset_value_per_share": 3.47,
      "current_price": 185.50,
      "margin_of_safety": -0.098
    },
    {
      "ticker": "MSFT",
      "dcf_fair_value_per_share": 380.45,
      "net_tangible_asset_value_per_share": 24.18,
      "current_price": 405.00,
      "margin_of_safety": -0.061
    },
    {
      "ticker": "GOOGL",
      "dcf_fair_value_per_share": 142.30,
      "net_tangible_asset_value_per_share": 89.22,
      "current_price": 155.00,
      "margin_of_safety": -0.082
    }
  ],
  "metadata": {
    "request_id": "req_123456",
    "processing_time_ms": 245,
    "timestamp": "2024-01-31T10:35:00Z"
  }
}
```

#### Health Check

```bash
GET /health
```

Returns service health status and dependencies:

```json
{
  "status": "healthy",
  "version": "2.5.0",
  "dependencies": {
    "database": "healthy",
    "redis": "healthy",
    "sec_api": "healthy",
    "yahoo_finance": "healthy"
  }
}
```

### Understanding the Valuation Results

#### Key Metrics Explained

- **DCF Fair Value per Share**: Intrinsic value based on 5-year discounted cash flow projection
- **Net Tangible Asset Value per Share**: Book value excluding intangibles, adjusted for market conditions
- **Margin of Safety**: Percentage difference between fair value and current price (negative = overvalued)
- **Quality Score**: 0-1 score indicating data reliability and adjustment transparency

#### Industry-Specific Adjustments

The API automatically applies industry-specific normalizations:

- **Technology**: R&D capitalization, stock-based compensation adjustments
- **Finance**: Regulatory capital requirements, credit loss provisions
- **Healthcare**: Drug development costs, regulatory milestone adjustments
- **Retail**: Inventory valuation, lease obligation adjustments

## ⚙️ Configuration

### Environment Variables

Create a `.env` file from the template:

```bash
cp config.env.example .env
```

Key configuration options:

```env
# API Configuration
PORT=8080
API_KEY=your-secure-api-key

# Data Sources
SEC_API_USER_AGENT=YourCompany your-email@example.com
YAHOO_FINANCE_TIMEOUT=30s

# Redis Cache
REDIS_URL=redis://localhost:6379
CACHE_TTL_FINANCIAL=24h
CACHE_TTL_MARKET=15m

# Rate Limiting
RATE_LIMIT_REQUESTS_PER_SECOND=10
RATE_LIMIT_BURST=20
```

### Cache Configuration

Midas uses intelligent caching to optimize performance:

- **Financial Data**: 24-hour TTL (quarterly reports)
- **Market Data**: 15-minute TTL (prices, volume)
- **Macro Data**: 7-day TTL (interest rates)

## 🔧 Operations

### Starting the Service

```bash
# Development mode with hot reload
go run cmd/server/main.go

# Production build
go build -o bin/midas cmd/server/main.go
./bin/midas

# Docker deployment
docker-compose up -d
```

### Stopping the Service

```bash
./scripts/stop_staging.sh  # Linux/macOS
# OR
.\scripts\stop_staging.ps1  # Windows
```

### Monitoring

- **Metrics**: Prometheus endpoint at `/metrics`
- **Health**: Health check at `/health`
- **Logs**: Structured JSON logging to stdout

## 🐛 Troubleshooting

### Common Issues

**"SEC rate limit exceeded"**
- The SEC API limits to 10 requests/second
- Solution: Enable Redis caching or reduce request rate

**"Market data unavailable"**
- Yahoo Finance may be temporarily down
- Solution: Cached data will be used if available

**"Invalid financial data"**
- Company may have unusual reporting structure
- Check the quality_score in response for details

### Debug Mode

Enable detailed logging:

```env
LOG_LEVEL=debug
DEBUG_MODE=true
```

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🤝 Support

- **Documentation**: [Full API Docs](http://localhost:8080/swagger/index.html)
- **Issues**: [GitHub Issues](https://github.com/your-org/midas/issues)

---

Built with ❤️ by the Midas Team